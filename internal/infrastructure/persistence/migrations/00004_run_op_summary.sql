-- A source's summary is written by a run of its own, so the audit's CHECK has to accept `summary`.
-- SQLite cannot change a CHECK constraint in place: the table is rebuilt (create → copy → drop →
-- rename), which keeps every existing row and every child row of run_steps.
--
-- NO TRANSACTION on purpose: `PRAGMA foreign_keys` is a no-op inside a transaction, and the DROP would
-- otherwise cascade into run_steps and delete the audit the table exists for. With the pragma off, the
-- children keep their run_id and the copy preserves every id.
-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;

CREATE TABLE runs_new (
    id                BLOB NOT NULL PRIMARY KEY CHECK (length(id) = 16),
    -- The class of work. Every pipeline run (document steps AND the cluster step) is a `pipeline` run;
    -- `step` says which step it executed. `tag` derives a wiki page's tags, `summary` a source's
    -- overview.
    op                TEXT NOT NULL CHECK (op IN ('pipeline','query','lint','vocabulary','tag','summary')),
    step              TEXT NOT NULL DEFAULT '',
    model             TEXT NOT NULL,
    target_id         BLOB,
    status            TEXT NOT NULL CHECK (status IN ('running','succeeded','failed','aborted')),
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens      INTEGER NOT NULL DEFAULT 0,
    iterations        INTEGER NOT NULL DEFAULT 0,
    answer            TEXT NOT NULL DEFAULT '',
    question          TEXT NOT NULL DEFAULT '',
    error             TEXT NOT NULL DEFAULT '',
    started_at        INTEGER NOT NULL,
    ended_at          INTEGER
);

INSERT INTO runs_new (
    id, op, step, model, target_id, status, prompt_tokens, completion_tokens, total_tokens,
    iterations, answer, question, error, started_at, ended_at
)
SELECT
    id, op, step, model, target_id, status, prompt_tokens, completion_tokens, total_tokens,
    iterations, answer, question, error, started_at, ended_at
FROM runs;

DROP TABLE runs;
ALTER TABLE runs_new RENAME TO runs;
CREATE INDEX IF NOT EXISTS idx_runs_op ON runs (op, started_at DESC);

PRAGMA foreign_keys = ON;

-- +goose Down
PRAGMA foreign_keys = OFF;

CREATE TABLE runs_old (
    id                BLOB NOT NULL PRIMARY KEY CHECK (length(id) = 16),
    op                TEXT NOT NULL CHECK (op IN ('pipeline','query','lint','vocabulary','tag')),
    step              TEXT NOT NULL DEFAULT '',
    model             TEXT NOT NULL,
    target_id         BLOB,
    status            TEXT NOT NULL CHECK (status IN ('running','succeeded','failed','aborted')),
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens      INTEGER NOT NULL DEFAULT 0,
    iterations        INTEGER NOT NULL DEFAULT 0,
    answer            TEXT NOT NULL DEFAULT '',
    question          TEXT NOT NULL DEFAULT '',
    error             TEXT NOT NULL DEFAULT '',
    started_at        INTEGER NOT NULL,
    ended_at          INTEGER
);

INSERT INTO runs_old (
    id, op, step, model, target_id, status, prompt_tokens, completion_tokens, total_tokens,
    iterations, answer, question, error, started_at, ended_at
)
SELECT
    id, op, step, model, target_id, status, prompt_tokens, completion_tokens, total_tokens,
    iterations, answer, question, error, started_at, ended_at
FROM runs WHERE op <> 'summary';

DROP TABLE runs;
ALTER TABLE runs_old RENAME TO runs;
CREATE INDEX IF NOT EXISTS idx_runs_op ON runs (op, started_at DESC);

PRAGMA foreign_keys = ON;
