package repository

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type openAIWeeklyResetSyncRepository struct {
	db *sql.DB
}

type openAIWeeklyResetTopologyRow struct {
	groupID         int64
	accountID       int64
	platform        string
	accountType     string
	status          string
	parentAccountID sql.NullInt64
	deletedAt       sql.NullTime
}

type openAIWeeklyCounterRow struct {
	id          int64
	usage       float64
	windowStart sql.NullTime
}

func NewOpenAIWeeklyResetSyncRepository(db *sql.DB) service.OpenAIWeeklyResetSyncRepository {
	return &openAIWeeklyResetSyncRepository{db: db}
}

func (r *openAIWeeklyResetSyncRepository) ListCandidates(ctx context.Context) (*service.OpenAIWeeklyResetCandidates, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("openai weekly reset sync repository db is nil")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT ag.group_id, a.id, a.platform, a.type, a.status, a.parent_account_id, a.deleted_at
		FROM account_groups ag
		JOIN groups g ON g.id = ag.group_id AND g.deleted_at IS NULL
		JOIN accounts a ON a.id = ag.account_id
		WHERE EXISTS (
			SELECT 1
			FROM account_groups scope_ag
			JOIN accounts scope_a ON scope_a.id = scope_ag.account_id
			WHERE scope_ag.group_id = ag.group_id
				AND scope_a.platform = $1
				AND scope_a.type = $2
		)
		ORDER BY ag.group_id, a.id
	`, service.PlatformOpenAI, service.AccountTypeOAuth)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	byGroup := make(map[int64][]openAIWeeklyResetTopologyRow)
	groupOrder := make([]int64, 0)
	for rows.Next() {
		var row openAIWeeklyResetTopologyRow
		if err := rows.Scan(&row.groupID, &row.accountID, &row.platform, &row.accountType, &row.status, &row.parentAccountID, &row.deletedAt); err != nil {
			return nil, err
		}
		if _, ok := byGroup[row.groupID]; !ok {
			groupOrder = append(groupOrder, row.groupID)
		}
		byGroup[row.groupID] = append(byGroup[row.groupID], row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := &service.OpenAIWeeklyResetCandidates{}
	seenAccounts := make(map[int64]struct{})
	for _, groupID := range groupOrder {
		accountID, reason, ok := classifyOpenAIWeeklyResetTopology(byGroup[groupID])
		if !ok {
			result.SkippedGroups = append(result.SkippedGroups, service.OpenAIWeeklyResetSkippedGroup{GroupID: groupID, Reason: reason})
			continue
		}
		if _, exists := seenAccounts[accountID]; exists {
			continue
		}
		seenAccounts[accountID] = struct{}{}
		result.AccountIDs = append(result.AccountIDs, accountID)
	}
	sort.Slice(result.AccountIDs, func(i, j int) bool { return result.AccountIDs[i] < result.AccountIDs[j] })
	return result, nil
}

func (r *openAIWeeklyResetSyncRepository) GetWeeklyRateLimitBypassCandidate(ctx context.Context, groupID int64) (*service.OpenAIWeeklyRateLimitBypassCandidate, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("openai weekly reset sync repository db is nil")
	}
	var platform, status string
	var enabled bool
	var windowStart sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT platform, status, weekly_rate_limit_bypass_enabled, weekly_rate_limit_bypass_window_start
		FROM groups
		WHERE id = $1 AND deleted_at IS NULL
	`, groupID).Scan(&platform, &status, &enabled, &windowStart)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGroupNotFound
	}
	if err != nil {
		return nil, err
	}
	if platform != service.PlatformOpenAI || status != service.StatusActive {
		return nil, service.ErrOpenAIWeeklyBypassUnsafeTopology
	}

	topology, err := r.openAIWeeklyResetGroupTopologyRead(ctx, groupID)
	if err != nil {
		return nil, err
	}
	accountID, _, ok := classifyOpenAIWeeklyResetTopology(topology)
	if !ok {
		return nil, service.ErrOpenAIWeeklyBypassUnsafeTopology
	}
	affected, err := countWeeklyLimitedAPIKeys(ctx, r.db, groupID)
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, service.ErrOpenAIWeeklyBypassNoLimitedKeys
	}
	return &service.OpenAIWeeklyRateLimitBypassCandidate{
		GroupID:             groupID,
		AccountID:           accountID,
		AffectedAPIKeyCount: affected,
		Enabled:             enabled,
		WindowStart:         nullTimePointer(windowStart),
	}, nil
}

func (r *openAIWeeklyResetSyncRepository) SetWeeklyRateLimitBypass(
	ctx context.Context,
	groupID, accountID int64,
	enabled bool,
	windowStart *time.Time,
) (_ *service.OpenAIWeeklyRateLimitBypassStatus, err error) {
	if r == nil || r.db == nil {
		return nil, errors.New("openai weekly reset sync repository db is nil")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	if enabled {
		eligible, lockErr := lockEligibleOpenAIWeeklyResetAccount(ctx, tx, accountID)
		if lockErr != nil {
			return nil, lockErr
		}
		if !eligible {
			return nil, service.ErrOpenAIWeeklyBypassUnsafeTopology
		}
	}

	var platform, groupStatus string
	var currentEnabled bool
	var currentWindow sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT platform, status, weekly_rate_limit_bypass_enabled, weekly_rate_limit_bypass_window_start
		FROM groups
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, groupID).Scan(&platform, &groupStatus, &currentEnabled, &currentWindow)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGroupNotFound
	}
	if err != nil {
		return nil, err
	}

	affected, err := countWeeklyLimitedAPIKeys(ctx, tx, groupID)
	if err != nil {
		return nil, err
	}
	if enabled {
		if platform != service.PlatformOpenAI || groupStatus != service.StatusActive || windowStart == nil || windowStart.IsZero() {
			return nil, service.ErrOpenAIWeeklyBypassUnsafeTopology
		}
		topology, topologyErr := openAIWeeklyResetGroupTopology(ctx, tx, groupID)
		if topologyErr != nil {
			return nil, topologyErr
		}
		topologyAccountID, _, ok := classifyOpenAIWeeklyResetTopology(topology)
		if !ok || topologyAccountID != accountID {
			return nil, service.ErrOpenAIWeeklyBypassUnsafeTopology
		}
		if affected == 0 {
			return nil, service.ErrOpenAIWeeklyBypassNoLimitedKeys
		}
		canonical := windowStart.UTC().Truncate(time.Second)
		currentStart := nullTimePointer(currentWindow)
		if currentEnabled && currentStart != nil && service.OpenAIWeeklyWindowsEquivalent(*currentStart, canonical) {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			tx = nil
			return weeklyBypassStatus(true, currentStart, affected, false), nil
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE groups
			SET weekly_rate_limit_bypass_enabled = TRUE,
				weekly_rate_limit_bypass_window_start = $1,
				updated_at = NOW()
			WHERE id = $2 AND deleted_at IS NULL
		`, canonical, groupID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		tx = nil
		return weeklyBypassStatus(true, &canonical, affected, true), nil
	}

	changed := currentEnabled || currentWindow.Valid
	if changed {
		if _, err := tx.ExecContext(ctx, `
			UPDATE groups
			SET weekly_rate_limit_bypass_enabled = FALSE,
				weekly_rate_limit_bypass_window_start = NULL,
				updated_at = NOW()
			WHERE id = $1 AND deleted_at IS NULL
		`, groupID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return weeklyBypassStatus(false, nil, affected, changed), nil
}

func weeklyBypassStatus(enabled bool, windowStart *time.Time, affected int, changed bool) *service.OpenAIWeeklyRateLimitBypassStatus {
	status := &service.OpenAIWeeklyRateLimitBypassStatus{
		Enabled:             enabled,
		WindowStart:         windowStart,
		AffectedAPIKeyCount: affected,
		Changed:             changed,
	}
	if enabled && windowStart != nil {
		autoCloseAt := windowStart.Add(service.RateLimitWindow7d)
		status.AutoCloseAt = &autoCloseAt
	}
	return status
}

func (r *openAIWeeklyResetSyncRepository) CloseExpiredOrUnsafeWeeklyRateLimitBypasses(ctx context.Context, now time.Time) ([]service.OpenAIWeeklyRateLimitBypassClosure, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("openai weekly reset sync repository db is nil")
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH candidates AS (
			SELECT g.id,
				CASE
					WHEN g.weekly_rate_limit_bypass_window_start IS NULL THEN 'missing_window_start'
					WHEN g.weekly_rate_limit_bypass_window_start + INTERVAL '7 days' <= $1 THEN 'deadline_reached'
					WHEN g.deleted_at IS NOT NULL OR g.status <> $2 OR g.platform <> $3 THEN 'group_inactive_or_platform_changed'
					WHEN (SELECT COUNT(*) FROM account_groups ag WHERE ag.group_id = g.id) <> 1 THEN 'multiple_or_mixed_accounts'
					WHEN NOT EXISTS (
						SELECT 1
						FROM account_groups ag
						JOIN accounts a ON a.id = ag.account_id
						WHERE ag.group_id = g.id
							AND a.deleted_at IS NULL
							AND a.platform = $3
							AND a.type = $4
							AND a.status = $2
							AND a.parent_account_id IS NULL
					) THEN 'unsafe_account_topology'
					ELSE NULL
				END AS reason
			FROM groups g
			WHERE g.weekly_rate_limit_bypass_enabled = TRUE
			FOR UPDATE SKIP LOCKED
		), closed AS (
			UPDATE groups g
			SET weekly_rate_limit_bypass_enabled = FALSE,
				weekly_rate_limit_bypass_window_start = NULL,
				updated_at = NOW()
			FROM candidates c
			WHERE g.id = c.id AND c.reason IS NOT NULL
			RETURNING g.id, c.reason
		)
		SELECT id, reason FROM closed ORDER BY id
	`, now.UTC(), service.StatusActive, service.PlatformOpenAI, service.AccountTypeOAuth)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []service.OpenAIWeeklyRateLimitBypassClosure
	for rows.Next() {
		var item service.OpenAIWeeklyRateLimitBypassClosure
		if err := rows.Scan(&item.GroupID, &item.Reason); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *openAIWeeklyResetSyncRepository) openAIWeeklyResetGroupTopologyRead(ctx context.Context, groupID int64) ([]openAIWeeklyResetTopologyRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT ag.group_id, a.id, a.platform, a.type, a.status, a.parent_account_id, a.deleted_at
		FROM account_groups ag
		JOIN accounts a ON a.id = ag.account_id
		WHERE ag.group_id = $1
		ORDER BY a.id
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var topology []openAIWeeklyResetTopologyRow
	for rows.Next() {
		var row openAIWeeklyResetTopologyRow
		if err := rows.Scan(&row.groupID, &row.accountID, &row.platform, &row.accountType, &row.status, &row.parentAccountID, &row.deletedAt); err != nil {
			return nil, err
		}
		topology = append(topology, row)
	}
	return topology, rows.Err()
}

type weeklyLimitedAPIKeyCounter interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func countWeeklyLimitedAPIKeys(ctx context.Context, queryer weeklyLimitedAPIKeyCounter, groupID int64) (int, error) {
	var count int
	err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM api_keys
		WHERE group_id = $1
			AND deleted_at IS NULL
			AND status = $2
			AND rate_limit_7d > 0
	`, groupID, service.StatusAPIKeyActive).Scan(&count)
	return count, err
}

func (r *openAIWeeklyResetSyncRepository) ReconcileWeeklyWindow(
	ctx context.Context,
	accountID int64,
	windowStart time.Time,
	forceInvalidate bool,
) (_ *service.OpenAIWeeklyResetReconcileResult, err error) {
	if r == nil || r.db == nil {
		return nil, errors.New("openai weekly reset sync repository db is nil")
	}
	windowStart = windowStart.UTC().Truncate(time.Second)
	if windowStart.IsZero() {
		return nil, errors.New("openai weekly reset sync window start is zero")
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	eligible, err := lockEligibleOpenAIWeeklyResetAccount(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	result := &service.OpenAIWeeklyResetReconcileResult{}
	if !eligible {
		return result, nil
	}

	groupIDs, err := openAIWeeklyResetAccountGroups(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	for _, groupID := range groupIDs {
		bypassEnabled, bypassWindowStart, err := lockOpenAIWeeklyResetGroup(ctx, tx, groupID)
		if err != nil {
			return nil, err
		}
		topology, err := openAIWeeklyResetGroupTopology(ctx, tx, groupID)
		if err != nil {
			return nil, err
		}
		topologyAccountID, reason, ok := classifyOpenAIWeeklyResetTopology(topology)
		if !ok || topologyAccountID != accountID {
			if reason == "" {
				reason = "account_topology_changed"
			}
			result.SkippedGroups = append(result.SkippedGroups, service.OpenAIWeeklyResetSkippedGroup{GroupID: groupID, Reason: reason})
			continue
		}

		// Usage billing locks subscriptions before API keys. Keep the same order
		// here so a reset racing a request cannot form a lock-order deadlock.
		if err := r.reconcileSubscriptions(ctx, tx, groupID, windowStart, forceInvalidate, result); err != nil {
			return nil, err
		}
		if err := r.reconcileAPIKeys(ctx, tx, groupID, windowStart, forceInvalidate, result); err != nil {
			return nil, err
		}
		if bypassEnabled && bypassWindowStart != nil && !service.OpenAIWeeklyWindowsEquivalent(*bypassWindowStart, windowStart) {
			if _, err := tx.ExecContext(ctx, `
				UPDATE groups
				SET weekly_rate_limit_bypass_enabled = FALSE,
					weekly_rate_limit_bypass_window_start = NULL,
					updated_at = NOW()
				WHERE id = $1 AND deleted_at IS NULL
			`, groupID); err != nil {
				return nil, err
			}
			result.AuthCacheGroupIDs = append(result.AuthCacheGroupIDs, groupID)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

func lockOpenAIWeeklyResetGroup(ctx context.Context, tx *sql.Tx, groupID int64) (bool, *time.Time, error) {
	var enabled bool
	var windowStart sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT weekly_rate_limit_bypass_enabled, weekly_rate_limit_bypass_window_start
		FROM groups
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, groupID).Scan(&enabled, &windowStart)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return enabled, nullTimePointer(windowStart), nil
}

func lockEligibleOpenAIWeeklyResetAccount(ctx context.Context, tx *sql.Tx, accountID int64) (bool, error) {
	var platform, accountType, status string
	var parentAccountID sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT platform, type, status, parent_account_id
		FROM accounts
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, accountID).Scan(&platform, &accountType, &status, &parentAccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return platform == service.PlatformOpenAI &&
		accountType == service.AccountTypeOAuth &&
		status == service.StatusActive &&
		!parentAccountID.Valid, nil
}

func openAIWeeklyResetAccountGroups(ctx context.Context, tx *sql.Tx, accountID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT ag.group_id
		FROM account_groups ag
		JOIN groups g ON g.id = ag.group_id
		WHERE ag.account_id = $1 AND g.deleted_at IS NULL
		ORDER BY ag.group_id
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var groupIDs []int64
	for rows.Next() {
		var groupID int64
		if err := rows.Scan(&groupID); err != nil {
			return nil, err
		}
		groupIDs = append(groupIDs, groupID)
	}
	return groupIDs, rows.Err()
}

func openAIWeeklyResetGroupTopology(ctx context.Context, tx *sql.Tx, groupID int64) ([]openAIWeeklyResetTopologyRow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT ag.group_id, a.id, a.platform, a.type, a.status, a.parent_account_id, a.deleted_at
		FROM account_groups ag
		JOIN accounts a ON a.id = ag.account_id
		WHERE ag.group_id = $1
		ORDER BY a.id
		FOR SHARE OF a
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var topology []openAIWeeklyResetTopologyRow
	for rows.Next() {
		var row openAIWeeklyResetTopologyRow
		if err := rows.Scan(&row.groupID, &row.accountID, &row.platform, &row.accountType, &row.status, &row.parentAccountID, &row.deletedAt); err != nil {
			return nil, err
		}
		topology = append(topology, row)
	}
	return topology, rows.Err()
}

func classifyOpenAIWeeklyResetTopology(rows []openAIWeeklyResetTopologyRow) (int64, string, bool) {
	if len(rows) != 1 {
		return 0, "multiple_or_mixed_accounts", false
	}
	account := rows[0]
	if account.platform != service.PlatformOpenAI || account.accountType != service.AccountTypeOAuth {
		return 0, "non_openai_oauth_account", false
	}
	if account.parentAccountID.Valid {
		return 0, "shadow_account", false
	}
	if account.deletedAt.Valid || account.status != service.StatusActive {
		return 0, "inactive_account", false
	}
	return account.accountID, "", true
}

func (r *openAIWeeklyResetSyncRepository) reconcileAPIKeys(
	ctx context.Context,
	tx *sql.Tx,
	groupID int64,
	windowStart time.Time,
	forceInvalidate bool,
	result *service.OpenAIWeeklyResetReconcileResult,
) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, usage_7d::float8, window_7d_start
		FROM api_keys
		WHERE group_id = $1 AND deleted_at IS NULL
		ORDER BY id
		FOR UPDATE
	`, groupID)
	if err != nil {
		return err
	}
	keys := make([]openAIWeeklyCounterRow, 0)
	for rows.Next() {
		var key openAIWeeklyCounterRow
		if err := rows.Scan(&key.id, &key.usage, &key.windowStart); err != nil {
			_ = rows.Close()
			return err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, key := range keys {
		changed, err := r.reconcileAPIKey(ctx, tx, key, windowStart)
		if err != nil {
			return err
		}
		if changed {
			result.UpdatedAPIKeys++
		}
		if changed || forceInvalidate {
			result.APIKeyIDs = append(result.APIKeyIDs, key.id)
		}
	}
	return nil
}

func (r *openAIWeeklyResetSyncRepository) reconcileAPIKey(ctx context.Context, tx *sql.Tx, key openAIWeeklyCounterRow, windowStart time.Time) (bool, error) {
	currentStart := nullTimePointer(key.windowStart)
	if currentStart != nil && service.OpenAIWeeklyWindowsEquivalent(*currentStart, windowStart) {
		return false, nil
	}
	historyCost, err := sumWeeklyResetUsage(ctx, tx, "api_key_id", key.id, currentStart, windowStart)
	if err != nil {
		return false, err
	}
	usage := adjustedWeeklyUsage(key.usage, currentStart, windowStart, historyCost)
	_, err = tx.ExecContext(ctx, `
		UPDATE api_keys
		SET usage_7d = $1, window_7d_start = $2
		WHERE id = $3 AND deleted_at IS NULL
	`, usage, windowStart, key.id)
	return err == nil, err
}

func (r *openAIWeeklyResetSyncRepository) reconcileSubscriptions(
	ctx context.Context,
	tx *sql.Tx,
	groupID int64,
	windowStart time.Time,
	forceInvalidate bool,
	result *service.OpenAIWeeklyResetReconcileResult,
) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, user_id, weekly_usage_usd::float8, weekly_window_start
		FROM user_subscriptions
		WHERE group_id = $1
			AND deleted_at IS NULL
			AND status = $2
			AND expires_at > NOW()
		ORDER BY id
		FOR UPDATE
	`, groupID, service.SubscriptionStatusActive)
	if err != nil {
		return err
	}
	type subscriptionRow struct {
		openAIWeeklyCounterRow
		userID int64
	}
	subscriptions := make([]subscriptionRow, 0)
	for rows.Next() {
		var sub subscriptionRow
		if err := rows.Scan(&sub.id, &sub.userID, &sub.usage, &sub.windowStart); err != nil {
			_ = rows.Close()
			return err
		}
		subscriptions = append(subscriptions, sub)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, sub := range subscriptions {
		currentStart := nullTimePointer(sub.windowStart)
		changed := currentStart == nil || !service.OpenAIWeeklyWindowsEquivalent(*currentStart, windowStart)
		if changed {
			historyCost, err := sumWeeklyResetUsage(ctx, tx, "subscription_id", sub.id, currentStart, windowStart)
			if err != nil {
				return err
			}
			usage := adjustedWeeklyUsage(sub.usage, currentStart, windowStart, historyCost)
			if _, err := tx.ExecContext(ctx, `
				UPDATE user_subscriptions
				SET weekly_usage_usd = $1, weekly_window_start = $2
				WHERE id = $3 AND deleted_at IS NULL
			`, usage, windowStart, sub.id); err != nil {
				return err
			}
			result.UpdatedSubscriptions++
		}
		if changed || forceInvalidate {
			result.SubscriptionCaches = append(result.SubscriptionCaches, service.OpenAIWeeklyResetSubscriptionCacheTarget{UserID: sub.userID, GroupID: groupID})
		}
	}
	return nil
}

// sumWeeklyResetUsage returns the immutable-log amount between the local and
// upstream boundaries. When the local boundary is nil, it returns all usage at
// or after the upstream boundary for first-time calibration.
func sumWeeklyResetUsage(ctx context.Context, tx *sql.Tx, idColumn string, id int64, currentStart *time.Time, windowStart time.Time) (float64, error) {
	var sinceQuery, rangeQuery string
	switch idColumn {
	case "api_key_id":
		sinceQuery = `SELECT COALESCE(SUM(actual_cost), 0)::float8 FROM usage_logs WHERE api_key_id = $1 AND created_at >= $2`
		rangeQuery = `SELECT COALESCE(SUM(actual_cost), 0)::float8 FROM usage_logs WHERE api_key_id = $1 AND created_at >= $2 AND created_at < $3`
	case "subscription_id":
		sinceQuery = `SELECT COALESCE(SUM(actual_cost), 0)::float8 FROM usage_logs WHERE subscription_id = $1 AND created_at >= $2`
		rangeQuery = `SELECT COALESCE(SUM(actual_cost), 0)::float8 FROM usage_logs WHERE subscription_id = $1 AND created_at >= $2 AND created_at < $3`
	default:
		return 0, errors.New("unsupported weekly reset usage id column")
	}
	if currentStart == nil {
		var amount float64
		err := tx.QueryRowContext(ctx, sinceQuery, id, windowStart).Scan(&amount)
		return amount, err
	}
	if currentStart.Equal(windowStart) {
		return 0, nil
	}
	start, end := windowStart, *currentStart
	if currentStart.Before(windowStart) {
		start, end = *currentStart, windowStart
	}
	var amount float64
	err := tx.QueryRowContext(ctx, rangeQuery, id, start, end).Scan(&amount)
	return amount, err
}

func adjustedWeeklyUsage(current float64, currentStart *time.Time, windowStart time.Time, historyCost float64) float64 {
	switch {
	case currentStart == nil:
		return maxZero(historyCost)
	case currentStart.Before(windowStart):
		return maxZero(current - historyCost)
	case currentStart.After(windowStart):
		return maxZero(current + historyCost)
	default:
		return maxZero(current)
	}
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	value.Time = value.Time.UTC()
	return &value.Time
}

func maxZero(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}
