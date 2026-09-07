package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUpstreamWeeklyQuotaAdmission(t *testing.T) {
	now := time.Now().UTC()
	start := now.Add(-time.Hour)
	key := &APIKey{UpstreamWeeklyLimitPercent: 25, RateLimit7d: 800, Usage7d: 9000}
	data := &APIKeyRateLimitData{UpstreamWeeklyUsagePercent: 6, UpstreamWeeklyWindowStart: &start, UpstreamWeeklyObservedAt: &now}
	require.Zero(t, key.EffectiveRateLimit7d())
	require.True(t, key.HasEnforcedRateLimits())
	require.NoError(t, checkUpstreamWeeklyQuota(key, data, now))
	data.UpstreamWeeklyUsagePercent = 25
	require.ErrorIs(t, checkUpstreamWeeklyQuota(key, data, now), ErrAPIKeyUpstreamWeeklyExceeded)
	require.ErrorIs(t, checkUpstreamWeeklyQuota(key, data, now.Add(11*time.Minute)), ErrAPIKeyUpstreamWeeklyUnavailable)
	require.ErrorIs(t, checkUpstreamWeeklyQuota(key, nil, now), ErrAPIKeyUpstreamWeeklyUnavailable)
	key.Group = &Group{WeeklyRateLimitBypassEnabled: true, WeeklyRateLimitBypassWindowStart: &start}
	require.NoError(t, checkUpstreamWeeklyQuota(key, data, now))
	require.False(t, key.HasEnforcedRateLimits())
}

func TestUpstreamWeeklyQuotaAuthCacheRoundTrip(t *testing.T) {
	svc := &APIKeyService{}
	key := &APIKey{ID: 1, UserID: 1, User: &User{ID: 1}, UpstreamWeeklyLimitPercent: 25, RateLimit7d: 800}
	snapshot := svc.snapshotFromAPIKey(context.Background(), key)
	require.NotNil(t, snapshot)
	require.Equal(t, 25.0, snapshot.UpstreamWeeklyLimitPercent)
}
