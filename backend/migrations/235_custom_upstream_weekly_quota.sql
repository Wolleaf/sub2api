-- Dollar billing remains immutable; this ledger allocates observed upstream
-- percentage increments to local keys. A manual dollar reset cannot grant a
-- second upstream allowance.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS rate_limit_reset_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS upstream_weekly_limit_percent NUMERIC(10,6) NOT NULL DEFAULT 0
        CHECK (upstream_weekly_limit_percent BETWEEN 0 AND 100),
    ADD COLUMN IF NOT EXISTS upstream_weekly_usage_percent NUMERIC(10,6) NOT NULL DEFAULT 0
        CHECK (upstream_weekly_usage_percent >= 0),
    ADD COLUMN IF NOT EXISTS upstream_weekly_window_start TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS upstream_weekly_observed_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS openai_weekly_quota_snapshots (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    window_start TIMESTAMPTZ NOT NULL,
    used_percent NUMERIC(10,6) NOT NULL DEFAULT 0,
    unattributed_percent NUMERIC(10,6) NOT NULL DEFAULT 0,
    costs JSONB NOT NULL DEFAULT '{}'::jsonb,
    observed_at TIMESTAMPTZ NOT NULL
);
