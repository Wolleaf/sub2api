//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type weeklyQuotaFixture struct {
	user, group, account int64
	keys                 []int64
	name                 string
}

func newWeeklyQuotaFixture(t *testing.T) weeklyQuotaFixture {
	t.Helper()
	ctx := context.Background()
	f := weeklyQuotaFixture{name: fmt.Sprintf("weekly-share-%d", time.Now().UnixNano())}
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES ($1,'test') RETURNING id`, f.name+"@example.test").Scan(&f.user))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform) VALUES ($1,'openai') RETURNING id`, f.name).Scan(&f.group))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status) VALUES ($1,'openai','oauth','active') RETURNING id`, f.name).Scan(&f.account))
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES ($1,$2)`, f.account, f.group)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		var id int64
		limit := 25
		if i == 2 {
			limit = 0
		} // unallocated key must consume reserve, not another key's share
		require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,key,name,group_id,rate_limit_7d,upstream_weekly_limit_percent)
		VALUES ($1,$2,$2,$3,800,$4) RETURNING id`, f.user, fmt.Sprintf("%s-%d", f.name, i), f.group, limit).Scan(&id))
		f.keys = append(f.keys, id)
	}
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM usage_logs WHERE account_id=$1`, f.account)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM api_keys WHERE user_id=$1`, f.user)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM account_groups WHERE group_id=$1`, f.group)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, f.account)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, f.group)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, f.user)
	})
	return f
}

func (f weeklyQuotaFixture) log(t *testing.T, key int, at time.Time, cost float64) {
	t.Helper()
	_, err := integrationDB.ExecContext(context.Background(), `INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,group_id,total_cost,actual_cost,created_at)
	VALUES ($1,$2,$3,$4,'gpt-5.6-sol',$5,$6,$6,$7)`, f.user, f.keys[key], f.account, fmt.Sprintf("%s-%d", f.name, time.Now().UnixNano()), f.group, cost, at)
	require.NoError(t, err)
}

func (f weeklyQuotaFixture) share(t *testing.T, key int) float64 {
	t.Helper()
	var used float64
	require.NoError(t, integrationDB.QueryRow(`SELECT upstream_weekly_usage_percent::float8 FROM api_keys WHERE id=$1`, f.keys[key]).Scan(&used))
	return used
}

func TestUpstreamWeeklyQuotaPersistsIncrementalSharesAndResets(t *testing.T) {
	ctx := context.Background()
	f := newWeeklyQuotaFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-2 * time.Hour)
	f.log(t, 0, start.Add(time.Minute), 80)
	f.log(t, 1, start.Add(time.Minute), 10)
	f.log(t, 2, start.Add(time.Minute), 10)
	r := &openAIWeeklyResetSyncRepository{db: integrationDB}
	require.NoError(t, r.ReconcileUpstreamWeeklyQuota(ctx, f.account, start, now, 5))
	require.InDelta(t, 4, f.share(t, 0), 1e-6)
	require.InDelta(t, .5, f.share(t, 1), 1e-6)
	// Production authentication must retain the percentage policy.
	key, err := NewAPIKeyRepository(integrationEntClient, integrationDB).GetByKeyForAuth(ctx, fmt.Sprintf("%s-0", f.name))
	require.NoError(t, err)
	require.Equal(t, 25.0, key.UpstreamWeeklyLimitPercent)
	require.Zero(t, key.EffectiveRateLimit7d())
	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET usage_7d=0,window_7d_start=NULL,rate_limit_reset_at=$1 WHERE id=$2`, now, f.keys[0])
	require.NoError(t, err)
	f.log(t, 0, now, 10)
	f.log(t, 1, now, 30)
	// No percentage movement: retain weights rather than assigning old usage again.
	require.NoError(t, r.ReconcileUpstreamWeeklyQuota(ctx, f.account, start.Add(3*time.Second), now.Add(time.Second), 5))
	require.InDelta(t, 4, f.share(t, 0), 1e-6)
	require.NoError(t, r.ReconcileUpstreamWeeklyQuota(ctx, f.account, start, now.Add(2*time.Second), 9))
	require.InDelta(t, 5, f.share(t, 0), 1e-6)
	require.InDelta(t, 3.5, f.share(t, 1), 1e-6)
	require.NoError(t, r.ReconcileUpstreamWeeklyQuota(ctx, f.account, start, now.Add(3*time.Second), 8))
	require.InDelta(t, 5, f.share(t, 0), 1e-6)
	var unattributed float64
	require.NoError(t, integrationDB.QueryRow(`SELECT unattributed_percent::float8 FROM openai_weekly_quota_snapshots WHERE account_id=$1`, f.account).Scan(&unattributed))
	require.InDelta(t, .5, unattributed, 1e-6)
	// A confirmed new upstream week resets all shares, not dollar history.
	newStart := now.Add(time.Minute)
	f.log(t, 1, newStart.Add(time.Second), 2)
	require.NoError(t, r.ReconcileUpstreamWeeklyQuota(ctx, f.account, newStart, now.Add(2*time.Minute), 1))
	require.Zero(t, f.share(t, 0))
	require.InDelta(t, 1, f.share(t, 1), 1e-6)
	require.NoError(t, r.ReconcileUpstreamWeeklyQuota(ctx, f.account, start, now.Add(3*time.Minute), 90))
	require.InDelta(t, 1, f.share(t, 1), 1e-6)
}

func TestOpenAIWeeklyManualDollarResetDoesNotSubtractOldHistory(t *testing.T) {
	ctx := context.Background()
	f := newWeeklyQuotaFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	upstream := now.Add(-time.Hour)
	reset := now.Add(-10 * time.Minute)
	f.log(t, 0, upstream.Add(-time.Hour), 1500)
	f.log(t, 0, reset.Add(time.Minute), 47.67)
	r := &openAIWeeklyResetSyncRepository{db: integrationDB}
	for _, beforeRequest := range []bool{false, true} {
		current := 47.67
		local := sql.NullTime{Time: upstream.Add(-2 * time.Hour), Valid: true}
		if beforeRequest {
			current = 0
			local = sql.NullTime{}
		}
		_, err := integrationDB.ExecContext(ctx, `UPDATE api_keys SET usage_7d=$1,window_7d_start=$2,rate_limit_reset_at=$3 WHERE id=$4`, current, local, reset, f.keys[0])
		require.NoError(t, err)
		tx, err := integrationDB.BeginTx(ctx, nil)
		require.NoError(t, err)
		changed, err := r.reconcileAPIKey(ctx, tx, openAIWeeklyCounterRow{id: f.keys[0], usage: current, windowStart: local, resetAt: sql.NullTime{Time: reset, Valid: true}}, upstream)
		if err != nil {
			_ = tx.Rollback()
		}
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, tx.Commit())
		var used float64
		require.NoError(t, integrationDB.QueryRow(`SELECT usage_7d::float8 FROM api_keys WHERE id=$1`, f.keys[0]).Scan(&used))
		require.InDelta(t, 47.67, used, 1e-8)
	}
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM usage_logs WHERE account_id=$1`, f.account).Scan(&n))
	require.Equal(t, 2, n)
	// Dollar resets also do not change the upstream policy.
	key, err := NewAPIKeyRepository(integrationEntClient, integrationDB).GetByID(ctx, f.keys[0])
	require.NoError(t, err)
	require.Equal(t, service.StatusActive, key.Status)
	require.Equal(t, 25.0, key.UpstreamWeeklyLimitPercent)
}
