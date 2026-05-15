package store

import (
	"context"
	"encoding/json"
	"time"
)

// WebhookEvent is a single GitHub webhook delivery, kept for diagnostics
// (/admin → Webhooks shows the last 50). The full payload is preserved
// so the admin can replay or inspect a missed event.
type WebhookEvent struct {
	ID         int64           `json:"id"`
	ReceivedAt time.Time       `json:"received_at"`
	DeliveryID *string         `json:"delivery_id,omitempty"`
	EventType  *string         `json:"event_type,omitempty"`
	Repo       *string         `json:"repo,omitempty"`
	HMACValid  *bool           `json:"hmac_valid,omitempty"`
	Payload    json.RawMessage `json:"payload"`
	Error      *string         `json:"error,omitempty"`
}

type RecordWebhookParams struct {
	DeliveryID string
	EventType  string
	Repo       string
	HMACValid  bool
	Payload    []byte
	Error      string
}

func (d *DB) RecordWebhook(ctx context.Context, p RecordWebhookParams) error {
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO webhook_events (delivery_id, event_type, repo, hmac_valid, payload, error)
		VALUES (NULLIF($1, ''), NULLIF($2, ''), NULLIF($3, ''), $4, $5, NULLIF($6, ''))
	`, p.DeliveryID, p.EventType, p.Repo, p.HMACValid, p.Payload, p.Error)
	return err
}

func (d *DB) ListRecentWebhooks(ctx context.Context, limit int) ([]WebhookEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := d.Pool.Query(ctx, `
		SELECT id, received_at, delivery_id, event_type, repo, hmac_valid, payload, error
		FROM webhook_events ORDER BY received_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WebhookEvent{}
	for rows.Next() {
		var e WebhookEvent
		if err := rows.Scan(&e.ID, &e.ReceivedAt, &e.DeliveryID, &e.EventType, &e.Repo, &e.HMACValid, &e.Payload, &e.Error); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
