BEGIN;

CREATE TABLE specialists (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    revision integer NOT NULL CHECK (revision > 0),
    enabled boolean NOT NULL DEFAULT true
);

CREATE TABLE specialist_proposals (
    id uuid PRIMARY KEY,
    specialist_id bigint REFERENCES specialists(id),
    base_revision integer NOT NULL,
    action text NOT NULL CHECK (action IN ('create', 'update', 'disable')),
    configuration jsonb NOT NULL,
    outcome text CHECK (outcome IN ('approve', 'decline', 'revise')),
    decided_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE specialist_revisions (
    specialist_id bigint NOT NULL REFERENCES specialists(id),
    revision integer NOT NULL,
    proposal_id uuid NOT NULL UNIQUE REFERENCES specialist_proposals(id),
    PRIMARY KEY (specialist_id, revision)
);

COMMIT;
