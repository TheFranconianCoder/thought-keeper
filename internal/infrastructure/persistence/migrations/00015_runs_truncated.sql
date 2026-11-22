-- A run that spent its research budget still answers, from what it had read. The flag is what keeps
-- that partial answer from looking complete: it is a run property, not an error, so the status stays
-- "succeeded" and the answer keeps its citations.
--
-- Additive with a default: existing rows are complete runs.
-- +goose Up
ALTER TABLE runs ADD COLUMN truncated INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE runs DROP COLUMN truncated;
