package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyWeeklyRateLimitBypassSkipsOnlySevenDayEnforcement(t *testing.T) {
	windowStart := time.Now().UTC().Add(-time.Hour)
	key := &APIKey{
		RateLimit7d: 800,
		Usage7d:     900,
		Group: &Group{
			WeeklyRateLimitBypassEnabled:     true,
			WeeklyRateLimitBypassWindowStart: &windowStart,
		},
	}

	require.True(t, key.HasRateLimits(), "configured usage counters must remain active")
	require.Zero(t, key.EffectiveRateLimit7d())
	require.False(t, key.HasEnforcedRateLimits())
	require.Equal(t, float64(800), key.RateLimit7d, "the persisted policy must not be mutated")
}

func TestAPIKeyWeeklyRateLimitBypassPreservesOtherWindows(t *testing.T) {
	windowStart := time.Now().UTC().Add(-time.Hour)
	key := &APIKey{
		RateLimit5h: 25,
		RateLimit7d: 800,
		Group: &Group{
			WeeklyRateLimitBypassEnabled:     true,
			WeeklyRateLimitBypassWindowStart: &windowStart,
		},
	}

	require.True(t, key.HasEnforcedRateLimits())
	require.Equal(t, float64(25), key.RateLimit5h)
	require.Zero(t, key.EffectiveRateLimit7d())
}

func TestAPIKeyWeeklyRateLimitBypassFailsSafeAfterDeadline(t *testing.T) {
	windowStart := time.Now().UTC().Add(-RateLimitWindow7d - time.Minute)
	key := &APIKey{
		RateLimit7d: 800,
		Group: &Group{
			WeeklyRateLimitBypassEnabled:     true,
			WeeklyRateLimitBypassWindowStart: &windowStart,
		},
	}

	require.Equal(t, float64(800), key.EffectiveRateLimit7d())
	require.True(t, key.HasEnforcedRateLimits())
}

func TestBillingEligibilityEvaluatorHonorsWeeklyBypassAndRestoresLimit(t *testing.T) {
	now := time.Now().UTC()
	windowStart := now.Add(-time.Hour)
	key := &APIKey{
		RateLimit7d: 800,
		Group: &Group{
			WeeklyRateLimitBypassEnabled:     true,
			WeeklyRateLimitBypassWindowStart: &windowStart,
		},
	}
	svc := &BillingCacheService{}

	require.NoError(t, svc.evaluateRateLimits(context.Background(), key, 0, 0, 900, &now, &now, &now))
	key.Group.WeeklyRateLimitBypassEnabled = false
	key.Group.WeeklyRateLimitBypassWindowStart = nil
	require.ErrorIs(t, svc.evaluateRateLimits(context.Background(), key, 0, 0, 900, &now, &now, &now), ErrAPIKeyRateLimit7dExceeded)
}
