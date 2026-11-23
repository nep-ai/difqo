BEGIN;

CREATE TABLE github_intake_cursors (
    repository_id bigint PRIMARY KEY CHECK (repository_id > 0),
    owner_id bigint NOT NULL CHECK (owner_id > 0),
    enrolled_at timestamptz NOT NULL,
    updated_through timestamptz NOT NULL CHECK (updated_through >= enrolled_at)
);

CREATE TABLE github_task_sources (
    repository_id bigint NOT NULL REFERENCES github_intake_cursors(repository_id),
    comment_id bigint NOT NULL CHECK (comment_id > 0),
    thread_number bigint NOT NULL CHECK (thread_number > 0),
    author_id bigint NOT NULL CHECK (author_id > 0),
    task_id bigint NOT NULL UNIQUE REFERENCES tasks(id),
    PRIMARY KEY (repository_id, comment_id)
);

CREATE INDEX github_task_sources_by_repo ON github_task_sources (repository_id, task_id);

COMMIT;
