package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSelectOpenAIWeeklyWindowStart(t *testing.T) {
	resetAt := time.Date(2026, 8, 20, 3, 33, 24, 0, time.UTC).Unix()
	want := time.Date(2026, 8, 13, 3, 33, 24, 0, time.UTC)
	weekly := func() *OpenAIRateLimitWindow {
		return &OpenAIRateLimitWindow{LimitWindowSeconds: openAIWeeklyWindowSeconds, ResetAt: resetAt}
	}
	fiveHours := &OpenAIRateLimitWindow{LimitWindowSeconds: 5 * 60 * 60, ResetAt: resetAt}

	tests := []struct {
		name    string
		usage   *OpenAIQuotaUsage
		wantErr error
	}{
		{name: "primary", usage: &OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{PrimaryWindow: weekly(), SecondaryWindow: fiveHours}}},
		{name: "secondary", usage: &OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{PrimaryWindow: fiveHours, SecondaryWindow: weekly()}}},
		{name: "missing rate limit", usage: &OpenAIQuotaUsage{}, wantErr: errOpenAIWeeklyWindowMissing},
		{name: "missing weekly", usage: &OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{PrimaryWindow: fiveHours}}, wantErr: errOpenAIWeeklyWindowMissing},
		{name: "ambiguous", usage: &OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{PrimaryWindow: weekly(), SecondaryWindow: weekly()}}, wantErr: errOpenAIWeeklyWindowAmbiguous},
		{name: "invalid reset", usage: &OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{PrimaryWindow: &OpenAIRateLimitWindow{LimitWindowSeconds: openAIWeeklyWindowSeconds, ResetAt: 1}}}, wantErr: errOpenAIWeeklyWindowInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectOpenAIWeeklyWindowStart(tt.usage)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

type openAIWeeklyResetRepoStub struct {
	candidates     *OpenAIWeeklyResetCandidates
	listErr        error
	reconcileFn    func(accountID int64, windowStart time.Time, force bool) (*OpenAIWeeklyResetReconcileResult, error)
	reconcileCalls int
}

func (s *openAIWeeklyResetRepoStub) ListCandidates(context.Context) (*OpenAIWeeklyResetCandidates, error) {
	return s.candidates, s.listErr
}

func (s *openAIWeeklyResetRepoStub) ReconcileWeeklyWindow(_ context.Context, accountID int64, windowStart time.Time, force bool) (*OpenAIWeeklyResetReconcileResult, error) {
	s.reconcileCalls++
	if s.reconcileFn == nil {
		return &OpenAIWeeklyResetReconcileResult{}, nil
	}
	return s.reconcileFn(accountID, windowStart, force)
}

type openAIWeeklyUsageReaderStub struct {
	usage *OpenAIQuotaUsage
	err   error
}

func (s *openAIWeeklyUsageReaderStub) QueryUsageSnapshot(context.Context, int64) (*OpenAIQuotaUsage, error) {
	return s.usage, s.err
}

type openAIWeeklyResetCacheStub struct {
	apiKeyCalls       []int64
	subscriptionCalls []OpenAIWeeklyResetSubscriptionCacheTarget
	publishCalls      []string
	failAPIKeyOnce    bool
}

func (s *openAIWeeklyResetCacheStub) InvalidateAPIKeyRateLimit(_ context.Context, keyID int64) error {
	s.apiKeyCalls = append(s.apiKeyCalls, keyID)
	if s.failAPIKeyOnce {
		s.failAPIKeyOnce = false
		return errors.New("redis unavailable")
	}
	return nil
}

func (s *openAIWeeklyResetCacheStub) InvalidateSubscription(_ context.Context, userID, groupID int64) error {
	s.subscriptionCalls = append(s.subscriptionCalls, OpenAIWeeklyResetSubscriptionCacheTarget{UserID: userID, GroupID: groupID})
	return nil
}

func (s *openAIWeeklyResetCacheStub) PublishSubscriptionCacheInvalidation(_ context.Context, cacheKey string) error {
	s.publishCalls = append(s.publishCalls, cacheKey)
	return nil
}

func TestOpenAIWeeklyResetSyncFailsClosedOnPollError(t *testing.T) {
	repo := &openAIWeeklyResetRepoStub{candidates: &OpenAIWeeklyResetCandidates{AccountIDs: []int64{7}}}
	svc := NewOpenAIWeeklyResetSyncService(repo, &openAIWeeklyUsageReaderStub{err: errors.New("upstream down")}, &openAIWeeklyResetCacheStub{}, time.Minute)

	require.NoError(t, svc.syncOnce(context.Background(), false))
	require.Zero(t, repo.reconcileCalls)
}

func TestOpenAIWeeklyResetSyncReconcilesAndInvalidates(t *testing.T) {
	resetAt := time.Date(2026, 8, 20, 3, 33, 24, 0, time.UTC).Unix()
	reader := &openAIWeeklyUsageReaderStub{usage: &OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{
		PrimaryWindow: &OpenAIRateLimitWindow{LimitWindowSeconds: openAIWeeklyWindowSeconds, ResetAt: resetAt},
	}}}
	repo := &openAIWeeklyResetRepoStub{candidates: &OpenAIWeeklyResetCandidates{AccountIDs: []int64{7}}}
	repo.reconcileFn = func(accountID int64, windowStart time.Time, force bool) (*OpenAIWeeklyResetReconcileResult, error) {
		require.Equal(t, int64(7), accountID)
		require.Equal(t, time.Date(2026, 8, 13, 3, 33, 24, 0, time.UTC), windowStart)
		require.True(t, force)
		return &OpenAIWeeklyResetReconcileResult{
			APIKeyIDs:          []int64{11, 12},
			SubscriptionCaches: []OpenAIWeeklyResetSubscriptionCacheTarget{{UserID: 21, GroupID: 31}},
		}, nil
	}
	cache := &openAIWeeklyResetCacheStub{}
	svc := NewOpenAIWeeklyResetSyncService(repo, reader, cache, time.Minute)

	require.NoError(t, svc.syncOnce(context.Background(), true))
	require.Equal(t, []int64{11, 12}, cache.apiKeyCalls)
	require.Equal(t, []OpenAIWeeklyResetSubscriptionCacheTarget{{UserID: 21, GroupID: 31}}, cache.subscriptionCalls)
	require.Equal(t, []string{subCacheKey(21, 31)}, cache.publishCalls)
}

func TestOpenAIWeeklyResetSyncRetriesCacheFailure(t *testing.T) {
	resetAt := time.Date(2026, 8, 20, 3, 33, 24, 0, time.UTC).Unix()
	reader := &openAIWeeklyUsageReaderStub{usage: &OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{
		SecondaryWindow: &OpenAIRateLimitWindow{LimitWindowSeconds: openAIWeeklyWindowSeconds, ResetAt: resetAt},
	}}}
	repo := &openAIWeeklyResetRepoStub{candidates: &OpenAIWeeklyResetCandidates{AccountIDs: []int64{7}}}
	repo.reconcileFn = func(_ int64, _ time.Time, _ bool) (*OpenAIWeeklyResetReconcileResult, error) {
		if repo.reconcileCalls == 1 {
			return &OpenAIWeeklyResetReconcileResult{APIKeyIDs: []int64{11}}, nil
		}
		return &OpenAIWeeklyResetReconcileResult{}, nil
	}
	cache := &openAIWeeklyResetCacheStub{failAPIKeyOnce: true}
	svc := NewOpenAIWeeklyResetSyncService(repo, reader, cache, time.Minute)

	require.NoError(t, svc.syncOnce(context.Background(), false))
	require.Equal(t, []int64{11}, cache.apiKeyCalls)
	require.Contains(t, svc.pendingAPIKeys, int64(11))

	require.NoError(t, svc.syncOnce(context.Background(), false))
	require.Equal(t, []int64{11, 11}, cache.apiKeyCalls)
	require.NotContains(t, svc.pendingAPIKeys, int64(11))
}
