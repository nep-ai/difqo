-- Drain and stop the relay before running. Set app_id and pilot_scope.
-- Keep pairing tombstones and event IDs to prevent reactivation or replay.
BEGIN;
SELECT set_config('difqo.pilot_app', :'app_id', true),
       set_config('difqo.pilot_scope', :'pilot_scope', true);

DO $$
BEGIN
    IF current_setting('difqo.pilot_app') !~ '^A[A-Z0-9]+$'
        OR current_setting('difqo.pilot_scope') !~ '^T[A-Z0-9]+:[UW][A-Z0-9]+$' THEN
        RAISE EXCEPTION 'Set a Slack app and pilot workspace:operator scope';
    END IF;
END $$;

UPDATE slack_relay_pairings
SET revoked = true, claimed = false, approval_pending = false,
    verified_workspace = '', verified_operator = '', installed_workspace = '',
    dm_channel = '', sealed_bot_token = NULL
WHERE app_id = current_setting('difqo.pilot_app')
    AND pilot_scope = current_setting('difqo.pilot_scope')
    AND NOT revoked;

DELETE FROM slack_relay_oauth_flows f
USING slack_relay_pairings p
WHERE f.app_id = p.app_id AND f.pairing_id = p.id
    AND p.app_id = current_setting('difqo.pilot_app')
    AND p.pilot_scope = current_setting('difqo.pilot_scope');

UPDATE slack_relay_events e
SET goal = NULL
FROM slack_relay_pairings p
WHERE e.app_id = p.app_id AND e.pairing_id = p.id
    AND p.app_id = current_setting('difqo.pilot_app')
    AND p.pilot_scope = current_setting('difqo.pilot_scope')
    AND e.goal IS NOT NULL;
COMMIT;
