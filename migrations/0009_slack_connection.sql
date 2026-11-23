BEGIN;

CREATE TABLE slack_connection (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    mode text NOT NULL CHECK (mode IN ('private', 'official', 'disconnected')),
    generation bigint NOT NULL CHECK (generation > 0),
    pairing jsonb
);

-- Leave the private binding and its replay records in their original tables.
INSERT INTO slack_connection (mode, generation) VALUES ('private', 1);

COMMIT;
