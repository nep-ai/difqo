BEGIN;

ALTER TABLE project_decisions
    ADD COLUMN project_id text NOT NULL DEFAULT '',
    ADD COLUMN project_binding text NOT NULL DEFAULT '',
    ADD CHECK ((project_id = '') = (project_binding = ''));

CREATE INDEX project_decisions_scope ON project_decisions (project_id, project_binding, id DESC);

CREATE TABLE specialist_runs (
    task_id bigint PRIMARY KEY REFERENCES tasks(id),
    request_id uuid NOT NULL UNIQUE,
    specialist_id bigint NOT NULL,
    revision integer NOT NULL,
    project jsonb NOT NULL,
    FOREIGN KEY (specialist_id, revision) REFERENCES specialist_revisions (specialist_id, revision)
);

CREATE INDEX specialist_runs_project ON specialist_runs ((project->>'id'), (project->>'binding'));

COMMIT;
