BEGIN;

ALTER TABLE slack_task_sources
    ADD COLUMN app_id text NOT NULL DEFAULT '',
    ADD COLUMN workspace_id text NOT NULL DEFAULT '';

UPDATE slack_task_sources
SET app_id = binding.app_id, workspace_id = binding.workspace_id
FROM slack_intake_binding AS binding
WHERE binding.singleton;

ALTER TABLE slack_task_sources
    ALTER COLUMN app_id DROP DEFAULT,
    ALTER COLUMN workspace_id DROP DEFAULT,
    DROP CONSTRAINT slack_task_sources_pkey,
    ADD PRIMARY KEY (app_id, workspace_id, event_id);

COMMIT;
