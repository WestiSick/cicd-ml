package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// SystemState mirrors the system_state table — a single-row key/value
// store for global flags like bootstrap_done, active_strategy, etc.
//
// Strict JSON-typed values keep the schema flexible without proliferating
// columns every time we add a setting. Reads always go through this
// package so the value types stay consistent.

const (
	keyBootstrapDone  = "bootstrap_done"
	keyActiveStrategy = "active_strategy"
	keyCustomWeights  = "custom_weights"
	keyGithubPAT      = "github_pat"
)

type ActiveModelSummary struct {
	ID      int64              `json:"id"`
	Name    string             `json:"name"`
	Algo    string             `json:"algo"`
	Metrics map[string]float64 `json:"metrics,omitempty"`
}

type CustomWeights struct {
	ShortJob          float64 `json:"short_job"`
	DeadlineProximity float64 `json:"deadline_proximity"`
	BranchImportance  float64 `json:"branch_importance"`
}

func DefaultCustomWeights() CustomWeights {
	return CustomWeights{
		ShortJob:          1.0,
		DeadlineProximity: 0.5,
		BranchImportance:  0.3,
	}
}

type SystemState struct {
	BootstrapDone  bool                `json:"bootstrap_done"`
	ActiveStrategy string              `json:"active_strategy,omitempty"`
	ActiveModel    *ActiveModelSummary `json:"active_model,omitempty"`
	CustomWeights  CustomWeights       `json:"custom_weights"`
}

// GetSystemState returns a snapshot of the bits the frontend cares about.
// Missing keys are treated as defaults — not errors — so a partially
// initialised DB still serves the API.
//
// Active model is joined in here (rather than a second round-trip from the
// frontend) because Dashboard KPIs reference both — keeping it in one
// payload avoids a flash of "—" on first load.
func (d *DB) GetSystemState(ctx context.Context) (SystemState, error) {
	out := SystemState{
		ActiveStrategy: "fifo",                 // default until the user picks one
		CustomWeights:  DefaultCustomWeights(), // default weights — wizard pre-fill
	}

	rows, err := d.Pool.Query(ctx, `SELECT key, value FROM system_state`)
	if err != nil {
		return SystemState{}, fmt.Errorf("query system_state: %w", err)
	}
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return SystemState{}, err
		}
		switch k {
		case keyBootstrapDone:
			_ = json.Unmarshal(v, &out.BootstrapDone)
		case keyActiveStrategy:
			var s string
			if err := json.Unmarshal(v, &s); err == nil && s != "" {
				out.ActiveStrategy = s
			}
		case keyCustomWeights:
			var w CustomWeights
			if err := json.Unmarshal(v, &w); err == nil {
				out.CustomWeights = w
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return SystemState{}, err
	}

	// Active model: best-effort. There's a partial unique index so at most
	// one row has is_active=TRUE; the join into the payload skips the
	// FeatureImportance blob which is too big for a KPI fetch.
	var (
		mid        int64
		mname      string
		malgo      string
		metricsRaw []byte
	)
	row := d.Pool.QueryRow(ctx, `
		SELECT id, name, algo, metrics
		FROM models WHERE is_active = TRUE
		LIMIT 1
	`)
	if err := row.Scan(&mid, &mname, &malgo, &metricsRaw); err == nil {
		metrics := map[string]float64{}
		_ = json.Unmarshal(metricsRaw, &metrics)
		out.ActiveModel = &ActiveModelSummary{
			ID: mid, Name: mname, Algo: malgo, Metrics: metrics,
		}
	}
	// pgx.ErrNoRows is the expected no-active-model case — leave nil.

	return out, nil
}

// SetActiveStrategy persists the strategy name into system_state.
// Validation of the strategy id happens at the http layer; this just writes.
func (d *DB) SetActiveStrategy(ctx context.Context, strategy string) error {
	val, _ := json.Marshal(strategy)
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO system_state (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, keyActiveStrategy, val)
	return err
}

// SetCustomWeights writes the weight triple used by the Custom strategy.
func (d *DB) SetCustomWeights(ctx context.Context, w CustomWeights) error {
	val, _ := json.Marshal(w)
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO system_state (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, keyCustomWeights, val)
	return err
}

// SetGithubPAT persists (or clears, when empty) the GitHub Personal Access
// Token used by the collector + webhook installer. Stored base64 — see the
// comment on the admin handler for why we don't use real encryption.
func (d *DB) SetGithubPAT(ctx context.Context, token string) error {
	if token == "" {
		_, err := d.Pool.Exec(ctx, `DELETE FROM system_state WHERE key = $1`, keyGithubPAT)
		return err
	}
	val, _ := json.Marshal(token)
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO system_state (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, keyGithubPAT, val)
	return err
}

// GetGithubPAT returns the persisted PAT or empty string if none. Callers
// should treat empty as "use unauthenticated GitHub API" (60 req/h).
func (d *DB) GetGithubPAT(ctx context.Context) (string, error) {
	var raw []byte
	err := d.Pool.QueryRow(ctx, `SELECT value FROM system_state WHERE key = $1`, keyGithubPAT).Scan(&raw)
	if err != nil {
		// pgx.ErrNoRows is fine — no token configured.
		return "", nil
	}
	var token string
	if err := json.Unmarshal(raw, &token); err != nil {
		return "", err
	}
	return token, nil
}

func (d *DB) SetBootstrapDone(ctx context.Context, done bool) error {
	val, _ := json.Marshal(done)
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO system_state (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, keyBootstrapDone, val)
	return err
}
