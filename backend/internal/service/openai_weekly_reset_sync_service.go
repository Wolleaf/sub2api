package service

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"
)

const (
	openAIWeeklyWindowSeconds    int64 = 7 * 24 * 60 * 60
	openAIWeeklySyncInterval           = 5 * time.Minute
	openAIWeeklySyncCycleTimeout       = 4 * time.Minute
)

var (
	errOpenAIWeeklyWindowMissing   = errors.New("openai weekly window is missing")
	errOpenAIWeeklyWindowAmbiguous = errors.New("openai weekly window is ambiguous")
	errOpenAIWeeklyWindowInvalid   = errors.New("openai weekly window is invalid")
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
	SkippedGroups        []OpenAIWeeklyResetSkippedGroup
	UpdatedAPIKeys       int
	UpdatedSubscriptions int
}

// OpenAIWeeklyResetSyncRepository owns the database transaction that
// revalidates topology and aligns weekly counters.
type OpenAIWeeklyResetSyncRepository interface {
	ListCandidates(ctx context.Context) (*OpenAIWeeklyResetCandidates, error)
	ReconcileWeeklyWindow(ctx context.Context, accountID int64, windowStart time.Time, forceInvalidate bool) (*OpenAIWeeklyResetReconcileResult, error)
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
	interval    time.Duration

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	pendingMu            sync.Mutex
	pendingAPIKeys       map[int64]struct{}
	pendingSubscriptions map[openAIWeeklyResetSubscriptionKey]struct{}
}

func NewOpenAIWeeklyResetSyncService(
	repo OpenAIWeeklyResetSyncRepository,
	usageReader openAIWeeklyUsageReader,
	cache openAIWeeklyResetCacheInvalidator,
	interval time.Duration,
) *OpenAIWeeklyResetSyncService {
	return &OpenAIWeeklyResetSyncService{
		repo:                 repo,
		usageReader:          usageReader,
		cache:                cache,
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

		result, err := s.repo.ReconcileWeeklyWindow(ctx, accountID, windowStart, forceInvalidate)
		if err != nil {
			slog.Warn("openai_weekly_reset_reconcile_failed", "account_id", accountID, "error", err)
			continue
		}
		if result == nil {
			continue
		}
		logOpenAIWeeklySkippedGroups(result.SkippedGroups)
		s.enqueueInvalidations(result)
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
