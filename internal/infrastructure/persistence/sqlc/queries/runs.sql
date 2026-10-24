-- name: InsertRun :exec
INSERT INTO runs (
    id, op, step, model, target_id, status, prompt_tokens, completion_tokens, total_tokens,
    iterations, question, answer, error, started_at, ended_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
);

-- name: FinishRun :exec
UPDATE runs SET
    status = ?, prompt_tokens = ?, completion_tokens = ?, total_tokens = ?,
    iterations = ?, answer = ?, error = ?, ended_at = ?
WHERE id = ?;

-- name: AbandonRunningRuns :exec
-- Close every run a PREVIOUS process left "running": the process that created those rows is gone, so
-- nothing is behind them. A list that keeps saying "running" is a list that lies.
UPDATE runs SET status = 'aborted', error = ?, ended_at = ?
WHERE status = 'running';

-- name: GetRun :one
SELECT * FROM runs WHERE id = ?;

-- name: InsertRunStep :exec
INSERT OR REPLACE INTO run_steps (run_id, seq, tool, args_digest, result_digest, is_error, duration_ms)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListRunSteps :many
SELECT run_id, seq, tool, args_digest, result_digest, is_error, duration_ms
FROM run_steps WHERE run_id = ? ORDER BY seq;
