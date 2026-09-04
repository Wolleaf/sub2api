//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWeeklyResetSyncReconcilesFromImmutableUsageLogs(t *testing.T) {
	ctx := context.Background()
	stamp := time.Now().UnixNano()
	name := fmt.Sprintf("weekly-sync-%d", stamp)
	targetStart := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	localStart := targetStart.Add(-24 * time.Hour)

	var userID, groupID, accountID, keyID, subscriptionID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash)
		VALUES ($1, 'test') RETURNING id
	`, name+"@example.test").Scan(&userID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO groups (name, platform, subscription_type, weekly_limit_usd)
		VALUES ($1, $2, 'subscription', 450) RETURNING id
	`, name, service.PlatformOpenAI).Scan(&groupID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO accounts (name, platform, type, status)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, name, service.PlatformOpenAI, service.AccountTypeOAuth, service.StatusActive).Scan(&accountID))
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO account_groups (account_id, group_id) VALUES ($1, $2)`, accountID, groupID)
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO api_keys (
			user_id, key, name, group_id, status,
			quota, quota_used, rate_limit_5h, rate_limit_1d, rate_limit_7d,
			usage_5h, usage_1d, usage_7d,
			window_5h_start, window_1d_start, window_7d_start
		)
		VALUES ($1, $2, $3, $4, 'active', 100, 77, 5, 10, 450, 3, 4, 15, $5, $6, $7)
		RETURNING id
	`, userID, "sk-"+name, name, groupID, targetStart.Add(-time.Hour), targetStart.Add(-time.Hour), localStart).Scan(&keyID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO user_subscriptions (
			user_id, group_id, starts_at, expires_at, status,
			daily_window_start, weekly_window_start, monthly_window_start,
			daily_usage_usd, weekly_usage_usd, monthly_usage_usd
		)
		VALUES ($1, $2, NOW() - INTERVAL '1 day', NOW() + INTERVAL '30 days', 'active', $3, $4, $5, 3, 15, 8)
		RETURNING id
	`, userID, groupID, targetStart.Add(-time.Hour), localStart, targetStart.Add(-time.Hour)).Scan(&subscriptionID))

	insertUsage := func(requestID string, createdAt time.Time, cost float64) {
		t.Helper()
		_, insertErr := integrationDB.ExecContext(ctx, `
			INSERT INTO usage_logs (
				user_id, api_key_id, account_id, request_id, model,
				group_id, subscription_id, total_cost, actual_cost, created_at
			)
			VALUES ($1, $2, $3, $4, 'gpt-5.6', $5, $6, $7, $7, $8)
		`, userID, keyID, accountID, requestID, groupID, subscriptionID, cost, createdAt)
		require.NoError(t, insertErr)
	}
	insertUsage(name+"-old", targetStart.Add(-12*time.Hour), 6)

	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM usage_logs WHERE request_id IN ($1, $2)`, name+"-old", name+"-new")
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM user_subscriptions WHERE id = $1`, subscriptionID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM api_keys WHERE id = $1`, keyID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM account_groups WHERE account_id = $1 AND group_id = $2`, accountID, groupID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id = $1`, accountID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM groups WHERE id = $1`, groupID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, userID)
	})

	type logSnapshot struct {
		count int64
		sum   float64
	}
	readLogSnapshot := func() logSnapshot {
		t.Helper()
		var snapshot logSnapshot
		require.NoError(t, integrationDB.QueryRowContext(ctx, `
			SELECT COUNT(*), COALESCE(SUM(actual_cost), 0)::float8
			FROM usage_logs WHERE request_id IN ($1, $2)
		`, name+"-old", name+"-new").Scan(&snapshot.count, &snapshot.sum))
		return snapshot
	}
	logsBefore := readLogSnapshot()

	repo := NewOpenAIWeeklyResetSyncRepository(integrationDB)
	candidates, err := repo.ListCandidates(ctx)
	require.NoError(t, err)
	require.Contains(t, candidates.AccountIDs, accountID)

	result, err := repo.ReconcileWeeklyWindow(ctx, accountID, targetStart, false)
	require.NoError(t, err)
	require.Equal(t, 1, result.UpdatedAPIKeys)
	require.Equal(t, 1, result.UpdatedSubscriptions)
	require.Equal(t, []int64{keyID}, result.APIKeyIDs)
	require.Equal(t, []service.OpenAIWeeklyResetSubscriptionCacheTarget{{UserID: userID, GroupID: groupID}}, result.SubscriptionCaches)

	var keyUsage, quotaUsed, usage5h, usage1d, rate5h, rate1d, rate7d float64
	var keyWindow time.Time
	var keyStatus string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT usage_7d::float8, window_7d_start, quota_used::float8,
			usage_5h::float8, usage_1d::float8,
			rate_limit_5h::float8, rate_limit_1d::float8, rate_limit_7d::float8, status
		FROM api_keys WHERE id = $1
	`, keyID).Scan(&keyUsage, &keyWindow, &quotaUsed, &usage5h, &usage1d, &rate5h, &rate1d, &rate7d, &keyStatus))
	require.InDelta(t, 9, keyUsage, 1e-9)
	require.True(t, keyWindow.Equal(targetStart))
	require.Equal(t, float64(77), quotaUsed)
	require.Equal(t, float64(3), usage5h)
	require.Equal(t, float64(4), usage1d)
	require.Equal(t, float64(5), rate5h)
	require.Equal(t, float64(10), rate1d)
	require.Equal(t, float64(450), rate7d)
	require.Equal(t, service.StatusAPIKeyActive, keyStatus)

	var weeklyUsage, dailyUsage, monthlyUsage float64
	var weeklyWindow time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT weekly_usage_usd::float8, weekly_window_start,
			daily_usage_usd::float8, monthly_usage_usd::float8
		FROM user_subscriptions WHERE id = $1
	`, subscriptionID).Scan(&weeklyUsage, &weeklyWindow, &dailyUsage, &monthlyUsage))
	require.InDelta(t, 9, weeklyUsage, 1e-9)
	require.True(t, weeklyWindow.Equal(targetStart))
	require.Equal(t, float64(3), dailyUsage)
	require.Equal(t, float64(8), monthlyUsage)
	require.Equal(t, logsBefore, readLogSnapshot())

	// Model the real billing/logging interleave: the billing transaction already
	// contributed $9 to both counters, while its best-effort immutable log lands
	// only after reset reconciliation. The delta algorithm preserved that $9.
	insertUsage(name+"-new", targetStart.Add(time.Hour), 9)
	require.Equal(t, logSnapshot{count: 2, sum: 15}, readLogSnapshot())

	// The same upstream identity is a true no-op after the first calibration.
	result, err = repo.ReconcileWeeklyWindow(ctx, accountID, targetStart, false)
	require.NoError(t, err)
	require.Zero(t, result.UpdatedAPIKeys)
	require.Zero(t, result.UpdatedSubscriptions)
	require.Empty(t, result.APIKeyIDs)
	require.Empty(t, result.SubscriptionCaches)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT usage_7d::float8 FROM api_keys WHERE id = $1`, keyID).Scan(&keyUsage))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT weekly_usage_usd::float8 FROM user_subscriptions WHERE id = $1`, subscriptionID).Scan(&weeklyUsage))
	require.InDelta(t, 9, keyUsage, 1e-9)
	require.InDelta(t, 9, weeklyUsage, 1e-9)

	// Startup mode invalidates caches even if the database is already aligned.
	result, err = repo.ReconcileWeeklyWindow(ctx, accountID, targetStart, true)
	require.NoError(t, err)
	require.Zero(t, result.UpdatedAPIKeys)
	require.Zero(t, result.UpdatedSubscriptions)
	require.Equal(t, []int64{keyID}, result.APIKeyIDs)
	require.Equal(t, []service.OpenAIWeeklyResetSubscriptionCacheTarget{{UserID: userID, GroupID: groupID}}, result.SubscriptionCaches)

	// Enabling the temporary bypass does not overwrite the configured $450
	// policy. Small upstream reset_at drift keeps both counters and the bypass
	// untouched; a genuinely new weekly window reconciles counters and closes it
	// in the same serializable transaction.
	candidate, err := repo.GetWeeklyRateLimitBypassCandidate(ctx, groupID)
	require.NoError(t, err)
	require.Equal(t, accountID, candidate.AccountID)
	require.Equal(t, 1, candidate.AffectedAPIKeyCount)

	status, err := repo.SetWeeklyRateLimitBypass(ctx, groupID, accountID, true, &targetStart)
	require.NoError(t, err)
	require.True(t, status.Enabled)
	require.True(t, status.Changed)

	var bypassEnabled bool
	var bypassStart time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT weekly_rate_limit_bypass_enabled, weekly_rate_limit_bypass_window_start, rate_limit_7d::float8
		FROM groups g
		JOIN api_keys k ON k.group_id = g.id
		WHERE g.id = $1 AND k.id = $2
	`, groupID, keyID).Scan(&bypassEnabled, &bypassStart, &rate7d))
	require.True(t, bypassEnabled)
	require.True(t, bypassStart.Equal(targetStart))
	require.Equal(t, float64(450), rate7d)

	result, err = repo.ReconcileWeeklyWindow(ctx, accountID, targetStart.Add(2*time.Second), false)
	require.NoError(t, err)
	require.Zero(t, result.UpdatedAPIKeys)
	require.Zero(t, result.UpdatedSubscriptions)
	require.Empty(t, result.AuthCacheGroupIDs)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT weekly_rate_limit_bypass_enabled FROM groups WHERE id = $1`, groupID).Scan(&bypassEnabled))
	require.True(t, bypassEnabled)

	newWeekStart := targetStart.Add(24 * time.Hour)
	result, err = repo.ReconcileWeeklyWindow(ctx, accountID, newWeekStart, false)
	require.NoError(t, err)
	require.Equal(t, []int64{groupID}, result.AuthCacheGroupIDs)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT weekly_rate_limit_bypass_enabled, rate_limit_7d::float8
		FROM groups g
		JOIN api_keys k ON k.group_id = g.id
		WHERE g.id = $1 AND k.id = $2
	`, groupID, keyID).Scan(&bypassEnabled, &rate7d))
	require.False(t, bypassEnabled)
	require.Equal(t, float64(450), rate7d)
}
