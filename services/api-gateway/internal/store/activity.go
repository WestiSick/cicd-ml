package store

import (
	"context"
	"encoding/json"
	"time"
)

// ActivityEntry — one row in activity_log. Surfaced verbatim on
// /admin → Activity log so the user has a paper trail of every action
// the system took on their behalf.
type ActivityEntry struct {
	ID      int64           `json:"id"`
	At      time.Time       `json:"at"`
	Actor   *string         `json:"actor,omitempty"`
	Action  string          `json:"action"`
	Target  *string         `json:"target,omitempty"`
	Success bool            `json:"success"`
	Message *string         `json:"message,omitempty"`
	Details json.RawMessage `json:"details"`
}

func (d *DB) RecordActivity(ctx context.Context, actor, action, target, message string, success bool, details any) error {
	raw := []byte("{}")
	if details != nil {
		if b, err := json.Marshal(details); err == nil {
			raw = b
		}
	}
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO activity_log (actor, action, target, success, message, details)
		VALUES (NULLIF($1, ''), $2, NULLIF($3, ''), $4, NULLIF($5, ''), $6)
	`, actor, action, target, success, message, raw)
	return err
}

func (d *DB) ListActivity(ctx context.Context, limit int) ([]ActivityEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.Pool.Query(ctx, `
		SELECT id, at, actor, action, target, success, message, details
		FROM activity_log
		ORDER BY at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActivityEntry{}
	for rows.Next() {
		var e ActivityEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.Target, &e.Success, &e.Message, &e.Details); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
