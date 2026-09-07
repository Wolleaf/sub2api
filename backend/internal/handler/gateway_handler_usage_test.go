package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type weeklyQuotaUsageRepo struct {
	service.APIKeyRepository
	data *service.APIKeyRateLimitData
	err  error
}

func (r *weeklyQuotaUsageRepo) GetRateLimitData(context.Context, int64) (*service.APIKeyRateLimitData, error) {
	return r.data, r.err
}

func TestUsageUpstreamWeeklyShare(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	start := now.Add(-time.Hour)
	for _, unavailable := range []bool{false, true} {
		repo := &weeklyQuotaUsageRepo{data: &service.APIKeyRateLimitData{
			Usage7d: 9000, UpstreamWeeklyUsagePercent: 6,
			UpstreamWeeklyWindowStart: &start, UpstreamWeeklyObservedAt: &now,
		}}
		if unavailable {
			repo.err = errors.New("database unavailable")
		}
		h := &GatewayHandler{apiKeyService: service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, nil)}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
		h.usageQuotaLimited(c, c.Request.Context(), &service.APIKey{
			ID: 1, Status: service.StatusAPIKeyActive, RateLimit7d: 800, UpstreamWeeklyLimitPercent: 25,
		}, nil, nil, nil)
		if unavailable {
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			continue
		}
		require.Equal(t, http.StatusOK, recorder.Code)
		var body map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		require.Equal(t, "percent", body["unit"])
		require.Equal(t, 19.0, body["remaining"])
		require.NotContains(t, body, "rate_limits")
		share, ok := body["upstream_weekly_quota"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, 25.0, share["limit"])
		require.Equal(t, 6.0, share["used"])
		require.Equal(t, true, share["estimated"])
		require.NotEmpty(t, share["reset_at"])
	}
}

func TestUsageUnrestrictedIncludesWeeklyWindowStart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)

	weeklyWindowStart := time.Date(2026, time.July, 13, 0, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	c.Set(string(middleware.ContextKeySubscription), &service.UserSubscription{
		WeeklyWindowStart: &weeklyWindowStart,
	})

	handler := &GatewayHandler{}
	handler.usageUnrestricted(
		c,
		context.Background(),
		&service.APIKey{Group: &service.Group{
			Name:             "Weekly plan",
			SubscriptionType: service.SubscriptionTypeSubscription,
		}},
		middleware.AuthSubject{},
		nil,
		nil,
		nil,
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Subscription struct {
			WeeklyWindowStart *time.Time `json:"weekly_window_start"`
		} `json:"subscription"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.NotNil(t, response.Subscription.WeeklyWindowStart)
	require.True(t, weeklyWindowStart.Equal(*response.Subscription.WeeklyWindowStart))
}
