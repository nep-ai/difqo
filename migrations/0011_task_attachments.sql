BEGIN;

ALTER TABLE tasks
    ADD COLUMN attachment_metadata jsonb,
    ADD COLUMN attachments jsonb;

COMMIT;
