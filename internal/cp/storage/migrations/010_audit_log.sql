-- +goose Up
-- Append-only audit_log per Issue 20 / PRD § audit_log. Every state-changing
-- request and every security-relevant ingest event lands here. The
-- correlation_id column joins related events from one request thread
-- (ADR-011) — e.g. the inbound auth.login row and the downstream
-- enrollment.anomaly row from the same operator session.
CREATE TABLE audit_log (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    at             timestamptz NOT NULL DEFAULT now(),
    action         text NOT NULL,
    actor_id       text NOT NULL DEFAULT '',
    actor_type     text NOT NULL,
    resource_kind  text NOT NULL DEFAULT '',
    resource_id    text NOT NULL DEFAULT '',
    correlation_id text NOT NULL DEFAULT '',
    source_ip      text NOT NULL DEFAULT '',
    user_agent     text NOT NULL DEFAULT '',
    outcome        text NOT NULL,
    payload        jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX audit_log_at_idx ON audit_log (at DESC);
CREATE INDEX audit_log_action_at_idx ON audit_log (action, at DESC);
CREATE INDEX audit_log_correlation_idx ON audit_log (correlation_id) WHERE correlation_id <> '';

-- +goose Down
DROP TABLE audit_log;
