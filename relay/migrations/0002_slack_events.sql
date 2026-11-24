BEGIN;

ALTER TABLE slack_relay_pairings
    ADD COLUMN claimed boolean NOT NULL DEFAULT false,
    ADD CHECK (NOT claimed OR (NOT revoked AND dm_channel <> ''));

CREATE UNIQUE INDEX slack_relay_one_active_dm
    ON slack_relay_pairings (app_id, verified_workspace, verified_operator, dm_channel)
    WHERE claimed;

CREATE TABLE slack_relay_events (
    app_id text NOT NULL,
    workspace_id text NOT NULL,
    event_id text NOT NULL,
    pairing_id text NOT NULL,
    goal text CHECK (char_length(goal) BETWEEN 1 AND 4096),
    queued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (app_id, workspace_id, event_id),
    FOREIGN KEY (app_id, pairing_id) REFERENCES slack_relay_pairings (app_id, id)
);

CREATE INDEX slack_relay_events_pairing_with_goal
    ON slack_relay_events (app_id, pairing_id) WHERE goal IS NOT NULL;

COMMIT;
