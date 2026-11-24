BEGIN;

ALTER TABLE slack_relay_pairings
    ADD COLUMN pilot_scope text NOT NULL DEFAULT '';

COMMIT;
