BEGIN;

ALTER TABLE chief_conversation_turns ADD COLUMN proposed_goal text;

CREATE TABLE chief_project_jobs (
    task_id bigint PRIMARY KEY REFERENCES tasks(id),
    turn_id bigint NOT NULL UNIQUE REFERENCES chief_conversation_turns(id),
    project jsonb NOT NULL
);

CREATE INDEX chief_project_jobs_binding ON chief_project_jobs ((project->>'execution_binding'), task_id);

COMMIT;
