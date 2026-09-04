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
	candidates         *OpenAIWeeklyResetCandidates
	listErr            error
	reconcileFn        func(accountID int64, windowStart time.Time, force bool) (*OpenAIWeeklyResetReconcileResult, error)
	reconcileCalls     int
	bypassCandidate    *OpenAIWeeklyRateLimitBypassCandidate
	bypassCandidateErr error
	setBypassFn        func(groupID, accountID int64, enabled bool, windowStart *time.Time) (*OpenAIWeeklyRateLimitBypassStatus, error)
	closures           []OpenAIWeeklyRateLimitBypassClosure
	closureErr         error
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

func (s *openAIWeeklyResetRepoStub) GetWeeklyRateLimitBypassCandidate(context.Context, int64) (*OpenAIWeeklyRateLimitBypassCandidate, error) {
	if s.bypassCandidateErr != nil {
		return nil, s.bypassCandidateErr
	}
	if s.bypassCandidate == nil {
		return nil, ErrOpenAIWeeklyBypassUnsafeTopology
	}
	return s.bypassCandidate, nil
}

func (s *openAIWeeklyResetRepoStub) SetWeeklyRateLimitBypass(_ context.Context, groupID, accountID int64, enabled bool, windowStart *time.Time) (*OpenAIWeeklyRateLimitBypassStatus, error) {
	if s.setBypassFn == nil {
		return nil, ErrOpenAIWeeklyBypassUnsafeTopology
	}
	return s.setBypassFn(groupID, accountID, enabled, windowStart)
}

func (s *openAIWeeklyResetRepoStub) CloseExpiredOrUnsafeWeeklyRateLimitBypasses(context.Context, time.Time) ([]OpenAIWeeklyRateLimitBypassClosure, error) {
	return s.closures, s.closureErr
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

type openAIWeeklyAuthCacheStub struct {
	groupIDs []int64
}

func (s *openAIWeeklyAuthCacheStub) InvalidateAuthCacheByKey(context.Context, string) {}

func (s *openAIWeeklyAuthCacheStub) InvalidateAuthCacheByUserID(context.Context, int64) {}

func (s *openAIWeeklyAuthCacheStub) InvalidateAuthCacheByGroupID(_ context.Context, groupID int64) {
	s.groupIDs = append(s.groupIDs, groupID)
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
	svc := NewOpenAIWeeklyResetSyncService(repo, &openAIWeeklyUsageReaderStub{err: errors.New("upstream down")}, &openAIWeeklyResetCacheStub{}, nil, time.Minute)

	require.NoError(t, svc.syncOnce(context.Background(), false))
	require.Zero(t, repo.reconcileCalls)
}

func TestOpenAIWeeklyResetSyncReconcilesAndInvalidates(t *testing.T) {
	expectedWindowStart := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	resetAt := expectedWindowStart.Add(RateLimitWindow7d).Unix()
	reader := &openAIWeeklyUsageReaderStub{usage: &OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{
		PrimaryWindow: &OpenAIRateLimitWindow{LimitWindowSeconds: openAIWeeklyWindowSeconds, ResetAt: resetAt},
	}}}
	repo := &openAIWeeklyResetRepoStub{candidates: &OpenAIWeeklyResetCandidates{AccountIDs: []int64{7}}}
	repo.reconcileFn = func(accountID int64, windowStart time.Time, force bool) (*OpenAIWeeklyResetReconcileResult, error) {
		require.Equal(t, int64(7), accountID)
		require.Equal(t, expectedWindowStart, windowStart)
		require.True(t, force)
		return &OpenAIWeeklyResetReconcileResult{
			APIKeyIDs:          []int64{11, 12},
			SubscriptionCaches: []OpenAIWeeklyResetSubscriptionCacheTarget{{UserID: 21, GroupID: 31}},
		}, nil
	}
	cache := &openAIWeeklyResetCacheStub{}
	svc := NewOpenAIWeeklyResetSyncService(repo, reader, cache, nil, time.Minute)

	require.NoError(t, svc.syncOnce(context.Background(), true))
	require.Equal(t, []int64{11, 12}, cache.apiKeyCalls)
	require.Equal(t, []OpenAIWeeklyResetSubscriptionCacheTarget{{UserID: 21, GroupID: 31}}, cache.subscriptionCalls)
	require.Equal(t, []string{subCacheKey(21, 31)}, cache.publishCalls)
}

func TestOpenAIWeeklyResetSyncRetriesCacheFailure(t *testing.T) {
	windowStart := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	resetAt := windowStart.Add(RateLimitWindow7d).Unix()
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
	svc := NewOpenAIWeeklyResetSyncService(repo, reader, cache, nil, time.Minute)

	require.NoError(t, svc.syncOnce(context.Background(), false))
	require.Equal(t, []int64{11}, cache.apiKeyCalls)
	require.Contains(t, svc.pendingAPIKeys, int64(11))

	require.NoError(t, svc.syncOnce(context.Background(), false))
	require.Equal(t, []int64{11, 11}, cache.apiKeyCalls)
	require.NotContains(t, svc.pendingAPIKeys, int64(11))
}

func TestOpenAIWeeklyWindowsEquivalentAllowsOnlyObservedJitter(t *testing.T) {
	base := time.Date(2026, 8, 13, 3, 33, 24, 0, time.UTC)
	require.True(t, OpenAIWeeklyWindowsEquivalent(base, base.Add(4*time.Minute+59*time.Second)))
	require.True(t, OpenAIWeeklyWindowsEquivalent(base, base.Add(-openAIWeeklyWindowJitterTolerance)))
	require.False(t, OpenAIWeeklyWindowsEquivalent(base, base.Add(5*time.Minute+time.Second)))
	require.False(t, OpenAIWeeklyWindowsEquivalent(time.Time{}, base))
}

func TestValidateOpenAIWeeklyWindowCurrentRejectsImplausibleTimestamps(t *testing.T) {
	now := time.Now().UTC()
	require.NoError(t, validateOpenAIWeeklyWindowCurrent(now.Add(-time.Hour), now))
	require.NoError(t, validateOpenAIWeeklyWindowCurrent(now.Add(4*time.Minute), now))
	require.ErrorIs(t, validateOpenAIWeeklyWindowCurrent(now.Add(6*time.Minute), now), errOpenAIWeeklyWindowInvalid)
	require.ErrorIs(t, validateOpenAIWeeklyWindowCurrent(now.Add(-RateLimitWindow7d-6*time.Minute), now), errOpenAIWeeklyWindowInvalid)
}

func TestOpenAIWeeklyRateLimitBypassEnableUsesFreshUpstreamWindow(t *testing.T) {
	windowStart := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	resetAt := windowStart.Add(RateLimitWindow7d).Unix()
	repo := &openAIWeeklyResetRepoStub{
		bypassCandidate: &OpenAIWeeklyRateLimitBypassCandidate{
			GroupID:             3,
			AccountID:           7,
			AffectedAPIKeyCount: 3,
		},
	}
	repo.setBypassFn = func(groupID, accountID int64, enabled bool, gotWindowStart *time.Time) (*OpenAIWeeklyRateLimitBypassStatus, error) {
		require.Equal(t, int64(3), groupID)
		require.Equal(t, int64(7), accountID)
		require.True(t, enabled)
		require.NotNil(t, gotWindowStart)
		require.Equal(t, windowStart, *gotWindowStart)
		return weeklyRateLimitBypassStatus(true, gotWindowStart, 3, true), nil
	}
	reader := &openAIWeeklyUsageReaderStub{usage: &OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{
		SecondaryWindow: &OpenAIRateLimitWindow{LimitWindowSeconds: openAIWeeklyWindowSeconds, ResetAt: resetAt},
	}}}
	authCache := &openAIWeeklyAuthCacheStub{}
	svc := NewOpenAIWeeklyResetSyncService(repo, reader, nil, authCache, time.Minute)

	status, err := svc.SetWeeklyRateLimitBypass(context.Background(), 3, true)
	require.NoError(t, err)
	require.True(t, status.Enabled)
	require.Equal(t, 3, status.AffectedAPIKeyCount)
	require.Equal(t, windowStart, *status.WindowStart)
	require.Equal(t, windowStart.Add(RateLimitWindow7d), *status.AutoCloseAt)
	require.Equal(t, []int64{3}, authCache.groupIDs)
}

func TestOpenAIWeeklyRateLimitBypassDisableDoesNotReadUpstream(t *testing.T) {
	repo := &openAIWeeklyResetRepoStub{}
	repo.setBypassFn = func(groupID, accountID int64, enabled bool, gotWindowStart *time.Time) (*OpenAIWeeklyRateLimitBypassStatus, error) {
		require.Equal(t, int64(3), groupID)
		require.Zero(t, accountID)
		require.False(t, enabled)
		require.Nil(t, gotWindowStart)
		return weeklyRateLimitBypassStatus(false, nil, 3, true), nil
	}
	authCache := &openAIWeeklyAuthCacheStub{}
	svc := NewOpenAIWeeklyResetSyncService(repo, nil, nil, authCache, time.Minute)

	status, err := svc.SetWeeklyRateLimitBypass(context.Background(), 3, false)
	require.NoError(t, err)
	require.False(t, status.Enabled)
	require.Nil(t, status.WindowStart)
	require.Equal(t, []int64{3}, authCache.groupIDs)
}

func TestOpenAIWeeklyResetSyncClosesDeadlineWithoutUpstream(t *testing.T) {
	repo := &openAIWeeklyResetRepoStub{
		closures:   []OpenAIWeeklyRateLimitBypassClosure{{GroupID: 3, Reason: "deadline"}},
		candidates: &OpenAIWeeklyResetCandidates{},
	}
	authCache := &openAIWeeklyAuthCacheStub{}
	svc := NewOpenAIWeeklyResetSyncService(repo, &openAIWeeklyUsageReaderStub{err: errors.New("upstream down")}, nil, authCache, time.Minute)

	require.NoError(t, svc.syncOnce(context.Background(), false))
	require.Equal(t, []int64{3}, authCache.groupIDs)
	require.Zero(t, repo.reconcileCalls)
}
