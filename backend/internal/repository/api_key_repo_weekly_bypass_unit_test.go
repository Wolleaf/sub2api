package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyRepository_GetByKeyForAuth_PreservesWeeklyRateLimitBypass_SQLite(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "getbykey-auth-weekly-bypass-unit@test.com")
	windowStart := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	group, err := client.Group.Create().
		SetName("g-auth-weekly-bypass-unit").
		SetPlatform(service.PlatformOpenAI).
		SetStatus(service.StatusActive).
		SetSubscriptionType(service.SubscriptionTypeStandard).
		SetRateMultiplier(1).
		SetWeeklyRateLimitBypassEnabled(true).
		SetWeeklyRateLimitBypassWindowStart(windowStart).
		Save(ctx)
	require.NoError(t, err)

	key := &service.APIKey{
		UserID:      user.ID,
		Key:         "sk-getbykey-auth-weekly-bypass-unit",
		Name:        "Weekly Bypass Key Unit",
		GroupID:     &group.ID,
		Status:      service.StatusActive,
		RateLimit7d: 800,
	}
	require.NoError(t, repo.Create(ctx, key))

	got, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.NotNil(t, got.Group)
	require.True(t, got.Group.WeeklyRateLimitBypassEnabled)
	require.NotNil(t, got.Group.WeeklyRateLimitBypassWindowStart)
	require.True(t, got.Group.WeeklyRateLimitBypassWindowStart.Equal(windowStart))
	require.Zero(t, got.EffectiveRateLimit7d(), "auth projection must activate the temporary weekly bypass")
}
