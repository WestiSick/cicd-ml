// Package ml is the HTTP client to the Python ml-service.
//
// We deliberately don't generate from OpenAPI — the contract is small and
// shared via the docs/architecture.md error envelope, not a binding. A
// tiny hand-rolled client keeps surface area minimal and the import graph
// in api-gateway flat.
package ml

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		// Training calls are bounded by ml-service's own timeout —
		// generous here because XGBoost on the upper end of our dataset
		// sizes can take 30–60s.
		HTTP: &http.Client{Timeout: 5 * time.Minute},
	}
}

// TrainRequest mirrors ml-service's POST /train body.
type TrainRequest struct {
	Algo          string         `json:"algo"`
	Params        map[string]any `json:"params,omitempty"`
	RepoIDs       []int64        `json:"repo_ids,omitempty"`
	Since         string         `json:"since,omitempty"`
	Name          string         `json:"name,omitempty"`
	TrainingJobID int64          `json:"training_job_id,omitempty"`
	Activate      bool           `json:"activate"`

	// ErrorWeighted: tier-2 continual learning. When true, ml-service
	// pulls the previous model's per-job prediction errors from the
	// `predictions` table and assigns higher sample_weight to rows it
	// got wrong. Pairs with the webhook-time per-(repo, workflow)
	// calibration for two-layer "learn from mistakes" behaviour.
	ErrorWeighted     bool    `json:"error_weighted,omitempty"`
	ErrorWeightAlpha  float64 `json:"error_weight_alpha,omitempty"`
}

type TrainResponse struct {
	ModelID    int64               `json:"model_id"`
	Algo       string              `json:"algo"`
	Name       string              `json:"name"`
	Metrics    map[string]float64  `json:"metrics"`
	TrainSize  int                 `json:"train_size"`
	TestSize   int                 `json:"test_size"`
	FeatureImp []FeatureImportance `json:"feature_importance"`
}

type FeatureImportance struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

func (c *Client) Train(ctx context.Context, req TrainRequest) (TrainResponse, error) {
	var resp TrainResponse
	if err := c.do(ctx, http.MethodPost, "/train/", req, &resp); err != nil {
		return TrainResponse{}, err
	}
	return resp, nil
}

// OptunaRequest matches ml-service POST /train/optuna body.
type OptunaRequest struct {
	Algo          string  `json:"algo"`
	NTrials       int     `json:"n_trials"`
	RepoIDs       []int64 `json:"repo_ids,omitempty"`
	Since         string  `json:"since,omitempty"`
	Name          string  `json:"name,omitempty"`
	TrainingJobID int64   `json:"training_job_id,omitempty"`
	Activate      bool    `json:"activate"`

	ErrorWeighted    bool    `json:"error_weighted,omitempty"`
	ErrorWeightAlpha float64 `json:"error_weight_alpha,omitempty"`
}

// TrainOptuna runs an Optuna hyperparameter search end-to-end:
// search → refit best params → persist model. The response mirrors
// TrainResponse with extra `best_params` and `n_trials` fields.
type OptunaResponse struct {
	TrainResponse
	NTrials     int                `json:"n_trials"`
	BestParams  map[string]any     `json:"best_params"`
	BestMetrics map[string]float64 `json:"best_metrics"`
}

func (c *Client) TrainOptuna(ctx context.Context, req OptunaRequest) (OptunaResponse, error) {
	var resp OptunaResponse
	if err := c.do(ctx, http.MethodPost, "/train/optuna", req, &resp); err != nil {
		return OptunaResponse{}, err
	}
	return resp, nil
}

// CVRequest matches ml-service POST /train/cv body. Walk-forward
// time-series CV — no model is persisted, returns per-fold + summary
// metrics so the UI can show "expected MAE = X ± σ" before the user
// commits to a full train.
type CVRequest struct {
	Algo    string         `json:"algo"`
	Params  map[string]any `json:"params,omitempty"`
	RepoIDs []int64        `json:"repo_ids,omitempty"`
	Since   string         `json:"since,omitempty"`
	NSplits int            `json:"n_splits"`
}

type CVResponse struct {
	Algo           string               `json:"algo"`
	NSplits        int                  `json:"n_splits"`
	FoldMetrics    []map[string]float64 `json:"fold_metrics"`
	MeanMetrics    map[string]float64   `json:"mean_metrics"`
	StdMetrics     map[string]float64   `json:"std_metrics"`
	TotalTrainSize int                  `json:"total_train_size"`
	TotalTestSize  int                  `json:"total_test_size"`
}

func (c *Client) CrossValidate(ctx context.Context, req CVRequest) (CVResponse, error) {
	var resp CVResponse
	if err := c.do(ctx, http.MethodPost, "/train/cv", req, &resp); err != nil {
		return CVResponse{}, err
	}
	return resp, nil
}

// BuildFeaturesRequest matches ml-service POST /features/build body.
type BuildFeaturesRequest struct {
	RepoIDs []int64 `json:"repo_ids,omitempty"`
}

type BuildFeaturesResponse struct {
	Jobs           int `json:"jobs"`
	Written        int `json:"written"`
	FeatureVersion int `json:"feature_version"`
	FeatureCount   int `json:"feature_count"`
}

// BuildFeatures triggers feature materialisation in ml-service. The
// compute_features bg_job uses this; users don't call it directly.
func (c *Client) BuildFeatures(ctx context.Context, req BuildFeaturesRequest) (BuildFeaturesResponse, error) {
	var resp BuildFeaturesResponse
	if err := c.do(ctx, http.MethodPost, "/features/build", req, &resp); err != nil {
		return BuildFeaturesResponse{}, err
	}
	return resp, nil
}

// PredictRequest covers both call modes in ml-service.
type PredictRequest struct {
	JobIDs []int64 `json:"job_ids,omitempty"`
	DryRun bool    `json:"dry_run,omitempty"`
}

type Prediction struct {
	JobID        int64   `json:"job_id"`
	PredictedSec float64 `json:"predicted_sec"`
}

type PredictResponse struct {
	ModelID     int64        `json:"model_id"`
	ModelAlgo   string       `json:"model_algo"`
	Predictions []Prediction `json:"predictions"`
}

func (c *Client) Predict(ctx context.Context, req PredictRequest) (PredictResponse, error) {
	var resp PredictResponse
	if err := c.do(ctx, http.MethodPost, "/predict/", req, &resp); err != nil {
		return PredictResponse{}, err
	}
	return resp, nil
}

// PredictFromPayloadRequest matches ml-service POST /predict/from-payload.
// Used by the webhook handler: at workflow_run.requested time we don't yet
// have a row in `jobs`, but we want an immediate predicted_sec to show on
// the dashboard. Mirrors the fields available on GitHub's workflow_run
// payload.
type PredictFromPayloadRequest struct {
	RepoOwner    string  `json:"repo_owner"`
	RepoName     string  `json:"repo_name"`
	WorkflowName *string `json:"workflow_name,omitempty"`
	HeadBranch   *string `json:"head_branch,omitempty"`
	Event        *string `json:"event,omitempty"`
	JobName      *string `json:"job_name,omitempty"`
	RunnerName   *string `json:"runner_name,omitempty"`
	StepsCount   *int    `json:"steps_count,omitempty"`

	// HeadSHA is the commit SHA from the workflow_run payload. When
	// supplied, ml-service joins commits + commit_files and feeds the
	// per-bucket file counts into the model — that's what lets the
	// webhook prediction know "this push is backend-heavy" vs "docs-
	// only". Optional: if empty, prediction falls back to repo/branch
	// averages only.
	HeadSHA *string `json:"head_sha,omitempty"`
}

type PredictFromPayloadResponse struct {
	ModelID      int64   `json:"model_id"`
	ModelAlgo    string  `json:"model_algo"`
	PredictedSec float64 `json:"predicted_sec"`
}

// PredictFromPayload is best-effort: callers should tolerate errors and
// proceed without a prediction rather than dropping the webhook. The
// most common error is `no_active_model` (returned as a *APIError with
// StatusCode 409) — this is normal on a fresh install.
func (c *Client) PredictFromPayload(ctx context.Context, req PredictFromPayloadRequest) (PredictFromPayloadResponse, error) {
	var resp PredictFromPayloadResponse
	if err := c.do(ctx, http.MethodPost, "/predict/from-payload", req, &resp); err != nil {
		return PredictFromPayloadResponse{}, err
	}
	return resp, nil
}

// ExportFiguresRequest matches ml-service POST /export/figures body.
type ExportFiguresRequest struct {
	Timestamp string `json:"timestamp"`
}

type ExportFiguresResponse struct {
	Directory     string   `json:"directory"`
	Files         []string `json:"files"`
	ActiveModelID *int64   `json:"active_model_id,omitempty"`
}

// ExportFigures triggers the PNG/PDF generation that lives in ml-service
// (matplotlib). Synchronous — the gateway already writes the CSV bundle
// in the same handler; chaining keeps the user-visible operation atomic.
func (c *Client) ExportFigures(ctx context.Context, req ExportFiguresRequest) (ExportFiguresResponse, error) {
	var resp ExportFiguresResponse
	if err := c.do(ctx, http.MethodPost, "/export/figures", req, &resp); err != nil {
		return ExportFiguresResponse{}, err
	}
	return resp, nil
}

// APIError is the decoded canonical envelope.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	UserAction string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("ml-service: %s: %s", e.Code, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("call %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var envelope struct {
			Error struct {
				Code       string `json:"code"`
				Message    string `json:"message"`
				UserAction string `json:"user_action"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&envelope)
		return &APIError{
			StatusCode: resp.StatusCode,
			Code:       envelope.Error.Code,
			Message:    envelope.Error.Message,
			UserAction: envelope.Error.UserAction,
		}
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
