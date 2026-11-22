BEGIN;

CREATE TABLE tasks (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    goal text NOT NULL CHECK (length(btrim(goal)) BETWEEN 1 AND 4096),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE attempts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id bigint NOT NULL UNIQUE REFERENCES tasks(id),
    claimed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz,
    exit_code integer,
    output text,
    CHECK ((completed_at IS NULL AND exit_code IS NULL AND output IS NULL)
        OR (completed_at IS NOT NULL AND exit_code IS NOT NULL AND output IS NOT NULL))
);

COMMIT;
