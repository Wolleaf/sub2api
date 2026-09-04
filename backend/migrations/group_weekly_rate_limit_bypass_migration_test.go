//go:build unit

package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration232AddsSafeWeeklyRateLimitBypassState(t *testing.T) {
	content, err := FS.ReadFile("232_group_weekly_rate_limit_bypass.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "weekly_rate_limit_bypass_enabled BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "weekly_rate_limit_bypass_window_start TIMESTAMPTZ")
	require.Contains(t, sql, "groups_weekly_rate_limit_bypass_state_check")
	require.Contains(t, sql, "weekly_rate_limit_bypass_enabled AND weekly_rate_limit_bypass_window_start IS NOT NULL")
	require.Contains(t, sql, "NOT weekly_rate_limit_bypass_enabled AND weekly_rate_limit_bypass_window_start IS NULL")
}

func TestMigration232ExtendsDurableAuthCacheInvalidation(t *testing.T) {
	content, err := FS.ReadFile("232_group_weekly_rate_limit_bypass.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION enqueue_group_auth_cache_invalidation")
	require.Contains(t, sql, "OLD.weekly_rate_limit_bypass_enabled IS NOT DISTINCT FROM NEW.weekly_rate_limit_bypass_enabled")
	require.Contains(t, sql, "OLD.weekly_rate_limit_bypass_window_start IS NOT DISTINCT FROM NEW.weekly_rate_limit_bypass_window_start")
	require.Contains(t, sql, "encode(sha256(convert_to(k.key, 'UTF8')), 'hex')")
	require.NotContains(t, sql, "INSERT INTO auth_cache_invalidation_outbox (raw_key)")
}
