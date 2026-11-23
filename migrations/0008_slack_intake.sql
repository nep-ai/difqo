BEGIN;

CREATE TABLE slack_intake_binding (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    app_id text NOT NULL,
    workspace_id text NOT NULL,
    operator_id text NOT NULL,
    channel_id text NOT NULL
);

CREATE TABLE slack_task_sources (
    event_id text PRIMARY KEY,
    task_id bigint NOT NULL UNIQUE REFERENCES tasks(id)
);

COMMIT;
