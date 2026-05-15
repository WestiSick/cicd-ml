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
)

type SystemState struct {
	BootstrapDone bool   `json:"bootstrap_done"`
	ActiveStrategy string `json:"active_strategy,omitempty"`
}

// GetSystemState returns a snapshot of the bits the frontend cares about.
// Missing keys are treated as defaults — not errors — so a partially
// initialised DB still serves the API.
func (d *DB) GetSystemState(ctx context.Context) (SystemState, error) {
	rows, err := d.Pool.Query(ctx, `SELECT key, value FROM system_state`)
	if err != nil {
		return SystemState{}, fmt.Errorf("query system_state: %w", err)
	}
	defer rows.Close()

	out := SystemState{}
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return SystemState{}, err
		}
		switch k {
		case keyBootstrapDone:
			_ = json.Unmarshal(v, &out.BootstrapDone)
		case keyActiveStrategy:
			_ = json.Unmarshal(v, &out.ActiveStrategy)
		}
	}
	return out, rows.Err()
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
