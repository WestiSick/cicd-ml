-- +goose Up
-- +goose StatementBegin

-- Per-model feature importance.
--
-- We store this as JSONB on the model row (rather than a separate
-- many-to-one table) for two reasons:
--   1. The set is small (< 200 features) and read together every time.
--   2. The column is opaque to SQL — there's no aggregate we'd ever
--      compute server-side. Treating it as a blob keeps reads cheap.
--
-- Shape:  {"feature_name": importance_value, ...}
-- Higher value = more important. Tree models report Gini importance,
-- Linear reports absolute coefficient magnitude.
ALTER TABLE models
  ADD COLUMN feature_importance JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE models DROP COLUMN IF EXISTS feature_importance;
-- +goose StatementEnd
