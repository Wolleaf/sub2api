package service

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	openAIWeeklyWindowSeconds         int64 = 7 * 24 * 60 * 60
	openAIWeeklySyncInterval                = 5 * time.Minute
	openAIWeeklySyncCycleTimeout            = 4 * time.Minute
	openAIWeeklyWindowJitterTolerance       = 5 * time.Minute
)

var (
	errOpenAIWeeklyWindowMissing   = errors.New("openai weekly window is missing")
	errOpenAIWeeklyWindowAmbiguous = errors.New("openai weekly window is ambiguous")
	errOpenAIWeeklyWindowInvalid   = errors.New("openai weekly window is invalid")

	ErrOpenAIWeeklyBypassUnsafeTopology = infraerrors.Conflict(
		"OPENAI_WEEKLY_BYPASS_UNSAFE_TOPOLOGY",
		"weekly rate-limit bypass requires exactly one active non-shadow OpenAI OAuth account",
	)
	ErrOpenAIWeeklyBypassNoLimitedKeys = infraerrors.Conflict(
		"OPENAI_WEEKLY_BYPASS_NO_LIMITED_KEYS",
		"group has no API key with a configured 7-day limit",
	)
)

// OpenAIWeeklyResetSkippedGroup is a sanitized topology diagnostic. It never
// contains account names, credentials, API keys, or upstream payloads.
type OpenAIWeeklyResetSkippedGroup struct {
	GroupID int64
	Reason  string
}

type OpenAIWeeklyResetCandidates struct {
	AccountIDs    []int64
	SkippedGroups []OpenAIWeeklyResetSkippedGroup
}

type OpenAIWeeklyResetSubscriptionCacheTarget struct {
	UserID  int64
	GroupID int64
}

type OpenAIWeeklyResetReconcileResult struct {
	APIKeyIDs            []int64
	SubscriptionCaches   []OpenAIWeeklyResetSubscriptionCacheTarget
	AuthCacheGroupIDs    []int64
	SkippedGroups        []OpenAIWeeklyResetSkippedGroup
	UpdatedAPIKeys       int
	UpdatedSubscriptions int
}

type OpenAIWeeklyRateLimitBypassCandidate struct {
	GroupID             int64
	AccountID           int64
	AffectedAPIKeyCount int
	Enabled             bool
	WindowStart         *time.Time
}

type OpenAIWeeklyRateLimitBypassStatus struct {
	Enabled             bool       `json:"enabled"`
	WindowStart         *time.Time `json:"window_start,omitempty"`
	AutoCloseAt         *time.Time `json:"auto_close_at,omitempty"`
	AffectedAPIKeyCount int        `json:"affected_api_key_count"`
	Changed             bool       `json:"-"`
}

type OpenAIWeeklyRateLimitBypassClosure struct {
	GroupID int64
	Reason  string
}

// OpenAIWeeklyResetSyncRepository owns the database transaction that
// revalidates topology and aligns weekly counters.
type OpenAIWeeklyResetSyncRepository interface {
	ListCandidates(ctx context.Context) (*OpenAIWeeklyResetCandidates, error)
	ReconcileWeeklyWindow(ctx context.Context, accountID int64, windowStart time.Time, forceInvalidate bool) (*OpenAIWeeklyResetReconcileResult, error)
	GetWeeklyRateLimitBypassCandidate(ctx context.Context, groupID int64) (*OpenAIWeeklyRateLimitBypassCandidate, error)
	SetWeeklyRateLimitBypass(ctx context.Context, groupID, accountID int64, enabled bool, windowStart *time.Time) (*OpenAIWeeklyRateLimitBypassStatus, error)
	CloseExpiredOrUnsafeWeeklyRateLimitBypasses(ctx context.Context, now time.Time) ([]OpenAIWeeklyRateLimitBypassClosure, error)
}

type openAIWeeklyUsageReader interface {
	QueryUsageSnapshot(ctx context.Context, accountID int64) (*OpenAIQuotaUsage, error)
}

type openAIWeeklyResetCacheInvalidator interface {
	InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error
	InvalidateSubscription(ctx context.Context, userID, groupID int64) error
	PublishSubscriptionCacheInvalidation(ctx context.Context, cacheKey string) error
}

type openAIWeeklyResetSubscriptionKey struct {
	userID  int64
	groupID int64
}

// OpenAIWeeklyResetSyncService polls the upstream weekly window and aligns only
// the downstream seven-day counters for single-account OpenAI OAuth groups.
type OpenAIWeeklyResetSyncService struct {
	repo        OpenAIWeeklyResetSyncRepository
	usageReader openAIWeeklyUsageReader
	cache       openAIWeeklyResetCacheInvalidator
	authCache   APIKeyAuthCacheInvalidator
	interval    time.Duration

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	pendingMu            sync.Mutex
	pendingAPIKeys       map[int64]struct{}
	pendingSubscriptions map[openAIWeeklyResetSubscriptionKey]struct{}
}

var openAIWeeklyResetObservationRegistry struct {
	sync.RWMutex
	service *OpenAIWeeklyResetSyncService
}

func NewOpenAIWeeklyResetSyncService(
	repo OpenAIWeeklyResetSyncRepository,
	usageReader openAIWeeklyUsageReader,
	cache openAIWeeklyResetCacheInvalidator,
	authCache APIKeyAuthCacheInvalidator,
	interval time.Duration,
) *OpenAIWeeklyResetSyncService {
	return &OpenAIWeeklyResetSyncService{
		repo:                 repo,
		usageReader:          usageReader,
		cache:                cache,
		authCache:            authCache,
		interval:             interval,
		stopCh:               make(chan struct{}),
		pendingAPIKeys:       make(map[int64]struct{}),
		pendingSubscriptions: make(map[openAIWeeklyResetSubscriptionKey]struct{}),
	}
}

func (s *OpenAIWeeklyResetSyncService) Start() {
	if s == nil || s.repo == nil || s.usageReader == nil || s.interval <= 0 {
		return
	}
	setOpenAIWeeklyResetObservationService(s)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		s.runOnce(true)
		for {
			select {
			case <-ticker.C:
				s.runOnce(false)
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *OpenAIWeeklyResetSyncService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
	clearOpenAIWeeklyResetObservationService(s)
}

func (s *OpenAIWeeklyResetSyncService) runOnce(forceInvalidate bool) {
	ctx, cancel := context.WithTimeout(context.Background(), openAIWeeklySyncCycleTimeout)
	defer cancel()
	if err := s.syncOnce(ctx, forceInvalidate); err != nil {
		slog.Warn("openai_weekly_reset_sync_failed", "error", err)
	}
}

func (s *OpenAIWeeklyResetSyncService) syncOnce(ctx context.Context, forceInvalidate bool) error {
	// Retry old cache failures even when the current upstream poll later fails.
	s.flushPendingInvalidations(ctx)
	closures, err := s.repo.CloseExpiredOrUnsafeWeeklyRateLimitBypasses(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, closure := range closures {
		s.invalidateAuthCacheForGroup(ctx, closure.GroupID)
		slog.Info("openai_weekly_rate_limit_bypass_closed",
			"group_id", closure.GroupID,
			"reason", closure.Reason,
		)
	}

	candidates, err := s.repo.ListCandidates(ctx)
	if err != nil {
		return err
	}
	if candidates == nil {
		return nil
	}
	logOpenAIWeeklySkippedGroups(candidates.SkippedGroups)

	for _, accountID := range candidates.AccountIDs {
		usage, err := s.usageReader.QueryUsageSnapshot(ctx, accountID)
		if err != nil {
			slog.Warn("openai_weekly_reset_poll_failed", "account_id", accountID, "error", err)
			continue
		}
		windowStart, err := selectOpenAIWeeklyWindowStart(usage)
		if err != nil {
			slog.Warn("openai_weekly_reset_window_rejected", "account_id", accountID, "error", err)
			continue
		}
		if err := validateOpenAIWeeklyWindowCurrent(windowStart, time.Now().UTC()); err != nil {
			slog.Warn("openai_weekly_reset_window_rejected", "account_id", accountID, "error", err)
			continue
		}

		result, err := s.repo.ReconcileWeeklyWindow(ctx, accountID, windowStart, forceInvalidate)
		if err != nil {
			slog.Warn("openai_weekly_reset_reconcile_failed", "account_id", accountID, "error", err)
			continue
		}
		if result == nil {
			continue
		}
		logOpenAIWeeklySkippedGroups(result.SkippedGroups)
		s.applyReconcileResult(ctx, result)
		if result.UpdatedAPIKeys > 0 || result.UpdatedSubscriptions > 0 {
			slog.Info("openai_weekly_reset_synced",
				"account_id", accountID,
				"window_start", windowStart.Format(time.RFC3339),
				"api_keys", result.UpdatedAPIKeys,
				"subscriptions", result.UpdatedSubscriptions,
			)
		}
	}

	s.flushPendingInvalidations(ctx)
	return nil
}

func (s *OpenAIWeeklyResetSyncService) applyReconcileResult(ctx context.Context, result *OpenAIWeeklyResetReconcileResult) {
	if s == nil || result == nil {
		return
	}
	s.enqueueInvalidations(result)
	for _, groupID := range result.AuthCacheGroupIDs {
		s.invalidateAuthCacheForGroup(ctx, groupID)
	}
}

func (s *OpenAIWeeklyResetSyncService) reconcileObservedWeeklyWindow(accountID int64, usage *OpenAIQuotaUsage) {
	if s == nil || s.repo == nil || usage == nil || accountID <= 0 {
		return
	}
	windowStart, err := selectOpenAIWeeklyWindowStart(usage)
	if err != nil {
		slog.Warn("openai_weekly_reset_post_credit_window_rejected", "account_id", accountID, "error", err)
		return
	}
	if err := validateOpenAIWeeklyWindowCurrent(windowStart, time.Now().UTC()); err != nil {
		slog.Warn("openai_weekly_reset_post_credit_window_rejected", "account_id", accountID, "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), openAIWeeklySyncCycleTimeout)
	defer cancel()
	result, err := s.repo.ReconcileWeeklyWindow(ctx, accountID, windowStart, false)
	if err != nil {
		slog.Warn("openai_weekly_reset_post_credit_reconcile_failed", "account_id", accountID, "error", err)
		return
	}
	s.applyReconcileResult(ctx, result)
	s.flushPendingInvalidations(ctx)
}

func setOpenAIWeeklyResetObservationService(s *OpenAIWeeklyResetSyncService) {
	openAIWeeklyResetObservationRegistry.Lock()
	openAIWeeklyResetObservationRegistry.service = s
	openAIWeeklyResetObservationRegistry.Unlock()
}

func clearOpenAIWeeklyResetObservationService(s *OpenAIWeeklyResetSyncService) {
	openAIWeeklyResetObservationRegistry.Lock()
	if openAIWeeklyResetObservationRegistry.service == s {
		openAIWeeklyResetObservationRegistry.service = nil
	}
	openAIWeeklyResetObservationRegistry.Unlock()
}

// NotifyOpenAIWeeklyResetObservation lets both manual and automatic reset-card
// flows immediately feed their already-fetched post-reset /wham/usage snapshot
// into the weekly reconciler. A nil or 5h-only snapshot cannot close a weekly
// bypass and is safely left to the normal poller.
func NotifyOpenAIWeeklyResetObservation(accountID int64, usage *OpenAIQuotaUsage) {
	if accountID <= 0 || usage == nil {
		return
	}
	openAIWeeklyResetObservationRegistry.RLock()
	svc := openAIWeeklyResetObservationRegistry.service
	openAIWeeklyResetObservationRegistry.RUnlock()
	if svc != nil {
		go svc.reconcileObservedWeeklyWindow(accountID, usage)
	}
}

// SetWeeklyRateLimitBypass is the admin mutation entry point. Enabling requires
// a fresh upstream weekly window; disabling never depends on upstream health.
func (s *OpenAIWeeklyResetSyncService) SetWeeklyRateLimitBypass(ctx context.Context, groupID int64, enabled bool) (*OpenAIWeeklyRateLimitBypassStatus, error) {
	if s == nil || s.repo == nil {
		return nil, infraerrors.ServiceUnavailable("OPENAI_WEEKLY_BYPASS_UNAVAILABLE", "weekly rate-limit bypass service is unavailable")
	}
	if !enabled {
		status, err := s.repo.SetWeeklyRateLimitBypass(ctx, groupID, 0, false, nil)
		if err != nil {
			return nil, err
		}
		if status != nil && status.Changed {
			s.invalidateAuthCacheForGroup(ctx, groupID)
		}
		return status, nil
	}
	if s.usageReader == nil {
		return nil, infraerrors.ServiceUnavailable("OPENAI_WEEKLY_BYPASS_UNAVAILABLE", "OpenAI usage reader is unavailable")
	}
	candidate, err := s.repo.GetWeeklyRateLimitBypassCandidate(ctx, groupID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if candidate.Enabled && candidate.WindowStart != nil && now.Before(candidate.WindowStart.Add(RateLimitWindow7d)) {
		return weeklyRateLimitBypassStatus(candidate.Enabled, candidate.WindowStart, candidate.AffectedAPIKeyCount, false), nil
	}
	usage, err := s.usageReader.QueryUsageSnapshot(ctx, candidate.AccountID)
	if err != nil {
		return nil, err
	}
	windowStart, err := selectOpenAIWeeklyWindowStart(usage)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("OPENAI_WEEKLY_BYPASS_WINDOW_INVALID", "OpenAI weekly window is unavailable").WithCause(err)
	}
	if err := validateOpenAIWeeklyWindowCurrent(windowStart, now); err != nil {
		return nil, infraerrors.ServiceUnavailable("OPENAI_WEEKLY_BYPASS_WINDOW_INVALID", "OpenAI weekly window is unavailable").WithCause(err)
	}
	if !now.Before(windowStart.Add(RateLimitWindow7d)) {
		return nil, infraerrors.ServiceUnavailable("OPENAI_WEEKLY_BYPASS_WINDOW_STALE", "OpenAI weekly window has already expired")
	}
	status, err := s.repo.SetWeeklyRateLimitBypass(ctx, groupID, candidate.AccountID, true, &windowStart)
	if err != nil {
		return nil, err
	}
	if status != nil && status.Changed {
		s.invalidateAuthCacheForGroup(ctx, groupID)
	}
	return status, nil
}

func (s *OpenAIWeeklyResetSyncService) GetWeeklyRateLimitBypassStatus(ctx context.Context, groupID int64) (*OpenAIWeeklyRateLimitBypassStatus, error) {
	if s == nil || s.repo == nil {
		return nil, infraerrors.ServiceUnavailable("OPENAI_WEEKLY_BYPASS_UNAVAILABLE", "weekly rate-limit bypass service is unavailable")
	}
	candidate, err := s.repo.GetWeeklyRateLimitBypassCandidate(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if candidate.Enabled {
		return weeklyRateLimitBypassStatus(true, candidate.WindowStart, candidate.AffectedAPIKeyCount, false), nil
	}
	if s.usageReader == nil {
		return nil, infraerrors.ServiceUnavailable("OPENAI_WEEKLY_BYPASS_UNAVAILABLE", "OpenAI usage reader is unavailable")
	}
	usage, err := s.usageReader.QueryUsageSnapshot(ctx, candidate.AccountID)
	if err != nil {
		return nil, err
	}
	windowStart, err := selectOpenAIWeeklyWindowStart(usage)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("OPENAI_WEEKLY_BYPASS_WINDOW_INVALID", "OpenAI weekly window is unavailable").WithCause(err)
	}
	now := time.Now().UTC()
	if err := validateOpenAIWeeklyWindowCurrent(windowStart, now); err != nil {
		return nil, infraerrors.ServiceUnavailable("OPENAI_WEEKLY_BYPASS_WINDOW_INVALID", "OpenAI weekly window is unavailable").WithCause(err)
	}
	if !now.Before(windowStart.Add(RateLimitWindow7d)) {
		return nil, infraerrors.ServiceUnavailable("OPENAI_WEEKLY_BYPASS_WINDOW_STALE", "OpenAI weekly window has already expired")
	}
	autoCloseAt := windowStart.Add(RateLimitWindow7d)
	return &OpenAIWeeklyRateLimitBypassStatus{
		Enabled:             false,
		WindowStart:         &windowStart,
		AutoCloseAt:         &autoCloseAt,
		AffectedAPIKeyCount: candidate.AffectedAPIKeyCount,
	}, nil
}

func weeklyRateLimitBypassStatus(enabled bool, windowStart *time.Time, affected int, changed bool) *OpenAIWeeklyRateLimitBypassStatus {
	status := &OpenAIWeeklyRateLimitBypassStatus{
		Enabled:             enabled,
		WindowStart:         windowStart,
		AffectedAPIKeyCount: affected,
		Changed:             changed,
	}
	if enabled && windowStart != nil {
		autoCloseAt := windowStart.Add(RateLimitWindow7d)
		status.AutoCloseAt = &autoCloseAt
	}
	return status
}

func (s *OpenAIWeeklyResetSyncService) invalidateAuthCacheForGroup(ctx context.Context, groupID int64) {
	if s == nil || s.authCache == nil || groupID <= 0 {
		return
	}
	s.authCache.InvalidateAuthCacheByGroupID(ctx, groupID)
}

func selectOpenAIWeeklyWindowStart(usage *OpenAIQuotaUsage) (time.Time, error) {
	if usage == nil || usage.RateLimit == nil {
		return time.Time{}, errOpenAIWeeklyWindowMissing
	}
	windows := make([]*OpenAIRateLimitWindow, 0, 2)
	for _, window := range []*OpenAIRateLimitWindow{usage.RateLimit.PrimaryWindow, usage.RateLimit.SecondaryWindow} {
		if window != nil && window.LimitWindowSeconds == openAIWeeklyWindowSeconds {
			windows = append(windows, window)
		}
	}
	if len(windows) == 0 {
		return time.Time{}, errOpenAIWeeklyWindowMissing
	}
	if len(windows) != 1 {
		return time.Time{}, errOpenAIWeeklyWindowAmbiguous
	}
	if windows[0].ResetAt <= openAIWeeklyWindowSeconds {
		return time.Time{}, errOpenAIWeeklyWindowInvalid
	}
	return time.Unix(windows[0].ResetAt-openAIWeeklyWindowSeconds, 0).UTC(), nil
}

// OpenAIWeeklyWindowsEquivalent absorbs the small reset_at drift observed from
// /wham/usage while still treating real early or natural resets as new windows.
func OpenAIWeeklyWindowsEquivalent(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return false
	}
	delta := a.Sub(b)
	if delta < 0 {
		delta = -delta
	}
	return delta <= openAIWeeklyWindowJitterTolerance
}

// validateOpenAIWeeklyWindowCurrent rejects structurally valid but implausible
// upstream timestamps. The tolerance covers the small clock/reset_at drift
// observed in production without allowing a malformed far-future window to
// extend a bypass indefinitely or rewrite local counters.
func validateOpenAIWeeklyWindowCurrent(windowStart, now time.Time) error {
	if windowStart.IsZero() || now.IsZero() {
		return errOpenAIWeeklyWindowInvalid
	}
	if windowStart.After(now.Add(openAIWeeklyWindowJitterTolerance)) {
		return errOpenAIWeeklyWindowInvalid
	}
	if !now.Before(windowStart.Add(RateLimitWindow7d + openAIWeeklyWindowJitterTolerance)) {
		return errOpenAIWeeklyWindowInvalid
	}
	return nil
}

func (s *OpenAIWeeklyResetSyncService) enqueueInvalidations(result *OpenAIWeeklyResetReconcileResult) {
	if s == nil || result == nil || s.cache == nil {
		return
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for _, keyID := range result.APIKeyIDs {
		s.pendingAPIKeys[keyID] = struct{}{}
	}
	for _, target := range result.SubscriptionCaches {
		s.pendingSubscriptions[openAIWeeklyResetSubscriptionKey{userID: target.UserID, groupID: target.GroupID}] = struct{}{}
	}
}

func (s *OpenAIWeeklyResetSyncService) flushPendingInvalidations(ctx context.Context) {
	if s == nil || s.cache == nil {
		return
	}
	s.pendingMu.Lock()
	keyIDs := make([]int64, 0, len(s.pendingAPIKeys))
	for keyID := range s.pendingAPIKeys {
		keyIDs = append(keyIDs, keyID)
	}
	subscriptions := make([]openAIWeeklyResetSubscriptionKey, 0, len(s.pendingSubscriptions))
	for target := range s.pendingSubscriptions {
		subscriptions = append(subscriptions, target)
	}
	s.pendingMu.Unlock()
	sort.Slice(keyIDs, func(i, j int) bool { return keyIDs[i] < keyIDs[j] })
	sort.Slice(subscriptions, func(i, j int) bool {
		if subscriptions[i].userID == subscriptions[j].userID {
			return subscriptions[i].groupID < subscriptions[j].groupID
		}
		return subscriptions[i].userID < subscriptions[j].userID
	})

	for _, keyID := range keyIDs {
		if err := s.cache.InvalidateAPIKeyRateLimit(ctx, keyID); err != nil {
			slog.Warn("openai_weekly_reset_api_key_cache_retry", "api_key_id", keyID, "error", err)
			continue
		}
		s.pendingMu.Lock()
		delete(s.pendingAPIKeys, keyID)
		s.pendingMu.Unlock()
	}
	for _, target := range subscriptions {
		if err := s.cache.InvalidateSubscription(ctx, target.userID, target.groupID); err != nil {
			slog.Warn("openai_weekly_reset_subscription_cache_retry", "user_id", target.userID, "group_id", target.groupID, "error", err)
			continue
		}
		if err := s.cache.PublishSubscriptionCacheInvalidation(ctx, subCacheKey(target.userID, target.groupID)); err != nil {
			slog.Warn("openai_weekly_reset_subscription_l1_cache_retry", "user_id", target.userID, "group_id", target.groupID, "error", err)
			continue
		}
		s.pendingMu.Lock()
		delete(s.pendingSubscriptions, target)
		s.pendingMu.Unlock()
	}
}

func logOpenAIWeeklySkippedGroups(groups []OpenAIWeeklyResetSkippedGroup) {
	for _, group := range groups {
		slog.Warn("openai_weekly_reset_group_skipped", "group_id", group.GroupID, "reason", group.Reason)
	}
}
