BEGIN;

ALTER TABLE attempts
    ADD COLUMN attempt_number integer NOT NULL DEFAULT 1 CHECK (attempt_number > 0),
    ADD COLUMN lease_expires_at timestamptz;

UPDATE attempts SET lease_expires_at = claimed_at;

ALTER TABLE attempts
    DROP CONSTRAINT attempts_task_id_key,
    ALTER COLUMN attempt_number DROP DEFAULT,
    ALTER COLUMN lease_expires_at SET NOT NULL,
    ADD UNIQUE (task_id, attempt_number);

COMMIT;
