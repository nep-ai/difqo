-- Invoke with psql -X -v ON_ERROR_STOP=1 and set app_id, pilot_scope,
-- and retention_hours. Keep event IDs for replay protection.
BEGIN;
SELECT set_config('difqo.pilot_app', :'app_id', true),
       set_config('difqo.pilot_scope', :'pilot_scope', true),
       set_config('difqo.retention_hours', :'retention_hours', true);

DO $$
BEGIN
    IF current_setting('difqo.pilot_app') !~ '^A[A-Z0-9]+$'
        OR current_setting('difqo.pilot_scope') !~ '^T[A-Z0-9]+:[UW][A-Z0-9]+$'
        OR current_setting('difqo.retention_hours')::integer NOT BETWEEN 1 AND 168 THEN
        RAISE EXCEPTION 'Set a Slack app, pilot workspace:operator scope, and retention of 1–168 hours';
    END IF;
END $$;

UPDATE slack_relay_events e
SET goal = NULL
FROM slack_relay_pairings p
WHERE e.app_id = p.app_id AND e.pairing_id = p.id
    AND p.app_id = current_setting('difqo.pilot_app')
    AND p.pilot_scope = current_setting('difqo.pilot_scope')
    AND e.goal IS NOT NULL
    AND e.queued_at < statement_timestamp()
        - make_interval(hours => current_setting('difqo.retention_hours')::integer);
COMMIT;
