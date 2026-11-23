BEGIN;

CREATE TABLE slack_relay_pairings (
    app_id text NOT NULL,
    id text NOT NULL CHECK (id ~ '^[0-9a-f]{32}$'),
    proof_hash bytea NOT NULL CHECK (octet_length(proof_hash) = 32),
    expires_at timestamptz NOT NULL,
    revoked boolean NOT NULL DEFAULT false,
    approval_pending boolean NOT NULL DEFAULT false,
    verified_workspace text NOT NULL DEFAULT '',
    verified_operator text NOT NULL DEFAULT '',
    installed_workspace text NOT NULL DEFAULT '',
    sealed_bot_token bytea,
    dm_channel text NOT NULL DEFAULT '',
    PRIMARY KEY (app_id, id),
    CHECK ((verified_workspace = '') = (verified_operator = '')),
    CHECK (verified_workspace = '' OR installed_workspace = '' OR verified_workspace = installed_workspace),
    CHECK (dm_channel = '' OR (verified_workspace <> '' AND installed_workspace = verified_workspace)),
    CHECK ((sealed_bot_token IS NOT NULL) = (installed_workspace <> '' AND dm_channel = ''))
);

CREATE TABLE slack_relay_oauth_flows (
    app_id text NOT NULL,
    pairing_id text NOT NULL,
    purpose text NOT NULL CHECK (purpose IN ('identity', 'install')),
    state_hash bytea NOT NULL CHECK (octet_length(state_hash) = 32),
    nonce text NOT NULL,
    consumed boolean NOT NULL DEFAULT false,
    PRIMARY KEY (app_id, pairing_id, purpose),
    UNIQUE (app_id, state_hash),
    FOREIGN KEY (app_id, pairing_id) REFERENCES slack_relay_pairings (app_id, id) ON DELETE CASCADE,
    CHECK ((purpose = 'identity') = (nonce <> ''))
);

COMMIT;
