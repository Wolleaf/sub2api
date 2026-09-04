-- Persist the admin-only, group-scoped weekly API-key rate-limit bypass.
-- The configured API-key limits and all usage counters remain untouched.

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS weekly_rate_limit_bypass_enabled BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS weekly_rate_limit_bypass_window_start TIMESTAMPTZ;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'groups_weekly_rate_limit_bypass_state_check'
          AND conrelid = 'groups'::regclass
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_weekly_rate_limit_bypass_state_check
            CHECK (
                (weekly_rate_limit_bypass_enabled AND weekly_rate_limit_bypass_window_start IS NOT NULL)
                OR
                (NOT weekly_rate_limit_bypass_enabled AND weekly_rate_limit_bypass_window_start IS NULL)
            );
    END IF;
END
$$;

COMMENT ON COLUMN groups.weekly_rate_limit_bypass_enabled IS
    'Temporarily bypass API-key 7-day USD limits for this group; configured limits and usage still persist';
COMMENT ON COLUMN groups.weekly_rate_limit_bypass_window_start IS
    'Canonical upstream OpenAI weekly window active when the bypass was enabled';

-- Group state is embedded in API-key auth snapshots. Extend the durable outbox
-- trigger so direct SQL changes made by the weekly synchronizer also invalidate
-- every affected key without storing plaintext credentials in the outbox.
CREATE OR REPLACE FUNCTION enqueue_group_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_group_id BIGINT;
BEGIN
    target_group_id := OLD.id;
    IF TG_OP = 'UPDATE'
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.is_exclusive IS NOT DISTINCT FROM NEW.is_exclusive
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at
       AND OLD.weekly_rate_limit_bypass_enabled IS NOT DISTINCT FROM NEW.weekly_rate_limit_bypass_enabled
       AND OLD.weekly_rate_limit_bypass_window_start IS NOT DISTINCT FROM NEW.weekly_rate_limit_bypass_window_start THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')
    FROM api_keys AS k
    WHERE k.group_id = target_group_id
      AND k.deleted_at IS NULL
      AND k.key <> '';
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;
