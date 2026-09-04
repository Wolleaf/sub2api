//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type weeklyBypassHandlerRepoStub struct {
	candidate      *service.OpenAIWeeklyRateLimitBypassCandidate
	setCalls       int
	setGroupID     int64
	setAccountID   int64
	setEnabled     bool
	setWindowStart *time.Time
}

func (s *weeklyBypassHandlerRepoStub) ListCandidates(context.Context) (*service.OpenAIWeeklyResetCandidates, error) {
	return &service.OpenAIWeeklyResetCandidates{}, nil
}

func (s *weeklyBypassHandlerRepoStub) ReconcileWeeklyWindow(context.Context, int64, time.Time, bool) (*service.OpenAIWeeklyResetReconcileResult, error) {
	return &service.OpenAIWeeklyResetReconcileResult{}, nil
}

func (s *weeklyBypassHandlerRepoStub) GetWeeklyRateLimitBypassCandidate(context.Context, int64) (*service.OpenAIWeeklyRateLimitBypassCandidate, error) {
	return s.candidate, nil
}

func (s *weeklyBypassHandlerRepoStub) SetWeeklyRateLimitBypass(_ context.Context, groupID, accountID int64, enabled bool, windowStart *time.Time) (*service.OpenAIWeeklyRateLimitBypassStatus, error) {
	s.setCalls++
	s.setGroupID = groupID
	s.setAccountID = accountID
	s.setEnabled = enabled
	s.setWindowStart = windowStart
	autoCloseAt := windowStart.Add(service.RateLimitWindow7d)
	return &service.OpenAIWeeklyRateLimitBypassStatus{
		Enabled:             enabled,
		WindowStart:         windowStart,
		AutoCloseAt:         &autoCloseAt,
		AffectedAPIKeyCount: s.candidate.AffectedAPIKeyCount,
	}, nil
}

func (s *weeklyBypassHandlerRepoStub) CloseExpiredOrUnsafeWeeklyRateLimitBypasses(context.Context, time.Time) ([]service.OpenAIWeeklyRateLimitBypassClosure, error) {
	return nil, nil
}

type weeklyBypassHandlerUsageStub struct {
	usage *service.OpenAIQuotaUsage
}

func (s *weeklyBypassHandlerUsageStub) QueryUsageSnapshot(context.Context, int64) (*service.OpenAIQuotaUsage, error) {
	return s.usage, nil
}

func setupWeeklyBypassHandlerRouter(repo *weeklyBypassHandlerRepoStub, reader *weeklyBypassHandlerUsageStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	syncService := service.NewOpenAIWeeklyResetSyncService(repo, reader, nil, nil, time.Minute)
	handler := NewGroupHandler(nil, nil, nil, syncService)
	router := gin.New()
	router.GET("/api/v1/admin/groups/:id/weekly-rate-limit-bypass", handler.GetWeeklyRateLimitBypass)
	router.PUT("/api/v1/admin/groups/:id/weekly-rate-limit-bypass", handler.UpdateWeeklyRateLimitBypass)
	return router
}

func TestGroupHandlerWeeklyRateLimitBypassContractDoesNotExposeAPIKeys(t *testing.T) {
	windowStart := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	repo := &weeklyBypassHandlerRepoStub{candidate: &service.OpenAIWeeklyRateLimitBypassCandidate{
		GroupID:             3,
		AccountID:           7,
		AffectedAPIKeyCount: 3,
	}}
	reader := &weeklyBypassHandlerUsageStub{usage: &service.OpenAIQuotaUsage{RateLimit: &service.OpenAIRateLimit{
		PrimaryWindow: &service.OpenAIRateLimitWindow{
			LimitWindowSeconds: int64(service.RateLimitWindow7d / time.Second),
			ResetAt:            windowStart.Add(service.RateLimitWindow7d).Unix(),
		},
	}}}
	router := setupWeeklyBypassHandlerRouter(repo, reader)

	for _, request := range []struct {
		method string
		body   string
	}{
		{method: http.MethodGet},
		{method: http.MethodPut, body: `{"enabled":true}`},
	} {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(request.method, "/api/v1/admin/groups/3/weekly-rate-limit-bypass", bytes.NewBufferString(request.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

		var envelope struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
		require.ElementsMatch(t,
			[]string{"enabled", "window_start", "auto_close_at", "affected_api_key_count"},
			mapKeys(envelope.Data),
		)
		require.NotContains(t, recorder.Body.String(), "api_keys")
		require.NotContains(t, recorder.Body.String(), "\"key\"")
		require.NotContains(t, recorder.Body.String(), "sk-")
	}
	require.Equal(t, 1, repo.setCalls)
	require.Equal(t, int64(3), repo.setGroupID)
	require.Equal(t, int64(7), repo.setAccountID)
	require.True(t, repo.setEnabled)
	require.NotNil(t, repo.setWindowStart)
	require.Equal(t, windowStart, *repo.setWindowStart)
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
