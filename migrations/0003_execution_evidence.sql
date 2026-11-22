BEGIN;

ALTER TABLE attempts
    ADD COLUMN blocked boolean NOT NULL DEFAULT false,
    ADD COLUMN artifacts text NOT NULL DEFAULT '',
    ADD CHECK (completed_at IS NOT NULL OR (NOT blocked AND artifacts = ''));

COMMIT;
