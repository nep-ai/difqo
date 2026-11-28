BEGIN;

CREATE TABLE chief_conversation_turns (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id text NOT NULL UNIQUE,
    message text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    attachment_metadata jsonb,
    attachments jsonb,
    state text NOT NULL CHECK (state IN ('running', 'completed', 'attention')),
    expires_at timestamptz NOT NULL,
    reply text NOT NULL DEFAULT '',
    problem text NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX chief_one_active_turn ON chief_conversation_turns ((true))
    WHERE state = 'running';

COMMIT;
