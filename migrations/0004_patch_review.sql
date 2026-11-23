BEGIN;

CREATE TABLE project_decisions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    body text NOT NULL CHECK (length(btrim(body)) BETWEEN 1 AND 4096)
);

ALTER TABLE tasks ADD COLUMN decision_id bigint REFERENCES project_decisions(id);
ALTER TABLE attempts ADD COLUMN snapshot jsonb;

CREATE TABLE review_checks (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    attempt_id bigint NOT NULL REFERENCES attempts(id),
    started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz,
    exit_code integer CHECK (exit_code BETWEEN 0 AND 255),
    output text NOT NULL DEFAULT '',
    UNIQUE (attempt_id, id),
    CHECK ((completed_at IS NULL) = (exit_code IS NULL))
);

CREATE TABLE patch_approvals (
    attempt_id bigint PRIMARY KEY,
    check_id bigint NOT NULL,
    approved_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (attempt_id, check_id) REFERENCES review_checks(attempt_id, id)
);

COMMIT;
