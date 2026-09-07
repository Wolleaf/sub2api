package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// ReconcileUpstreamWeeklyQuota allocates only newly observed percentage points.
// Dollar logs supply weights, not an assumed dollar capacity for the account.
// Allocated shares never move between keys within a week. Unattributable usage
// stays with the account reserve, and manual dollar resets do not touch shares.
func (r *openAIWeeklyResetSyncRepository) ReconcileUpstreamWeeklyQuota(ctx context.Context, accountID int64, start, observedAt time.Time, used float64) error {
	if math.IsNaN(used) || math.IsInf(used, 0) || used < 0 || used > 100 {
		return errors.New("invalid upstream weekly used percentage")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	ok, err := lockEligibleOpenAIWeeklyResetAccount(ctx, tx, accountID)
	if err != nil || !ok {
		return err
	}
	groups, err := openAIWeeklyResetAccountGroups(ctx, tx, accountID)
	if err != nil {
		return err
	}
	eligible := make([]int64, 0, len(groups))
	for _, groupID := range groups {
		if _, _, err := lockOpenAIWeeklyResetGroup(ctx, tx, groupID); err != nil {
			return err
		}
		topology, err := openAIWeeklyResetGroupTopology(ctx, tx, groupID)
		if err != nil {
			return err
		}
		id, _, safe := classifyOpenAIWeeklyResetTopology(topology)
		if safe && id == accountID {
			eligible = append(eligible, groupID)
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, upstream_weekly_usage_percent::float8, upstream_weekly_window_start
		FROM api_keys WHERE group_id = ANY($1) AND deleted_at IS NULL
		AND upstream_weekly_limit_percent > 0 ORDER BY id FOR UPDATE`, pq.Array(eligible))
	if err != nil {
		return err
	}
	type keyShare struct {
		id    int64
		used  float64
		start sql.NullTime
	}
	keys := []keyShare{}
	for rows.Next() {
		var k keyShare
		if err := rows.Scan(&k.id, &k.used, &k.start); err != nil {
			_ = rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}

	var previousStart, previousObserved time.Time
	var previousUsed, unattributed float64
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT window_start, used_percent::float8,
		unattributed_percent::float8, costs, observed_at
		FROM openai_weekly_quota_snapshots WHERE account_id=$1 FOR UPDATE`, accountID).
		Scan(&previousStart, &previousUsed, &unattributed, &raw, &previousObserved)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	previousCosts := map[int64]float64{}
	if err == nil {
		if observedAt.Before(previousObserved) {
			return nil
		}
		if service.OpenAIWeeklyWindowsEquivalent(start, previousStart) {
			start = previousStart // absorb upstream timestamp jitter
			if err := json.Unmarshal(raw, &previousCosts); err != nil {
				return err
			}
		} else {
			if start.Before(previousStart) {
				return nil
			} // stale old-week response
			previousUsed, unattributed = 0, 0
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT api_key_id, COALESCE(SUM(total_cost),0)::float8
		FROM usage_logs WHERE account_id=$1 AND created_at >= $2 AND created_at <= $3
		GROUP BY api_key_id`, accountID, start, observedAt)
	if err != nil {
		return err
	}
	costs := map[int64]float64{}
	for rows.Next() {
		var id int64
		var cost float64
		if err := rows.Scan(&id, &cost); err != nil {
			_ = rows.Close()
			return err
		}
		costs[id] = cost
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	// Regressive percentages never refund a share. Only a confirmed new week
	// starts a new allowance. Keep uncharged log weights until the next increase.
	delta := math.Max(0, used-previousUsed)
	shares := allocateUpstreamWeeklyDelta(delta, previousCosts, costs)
	allocated := 0.0
	for _, k := range keys {
		base := k.used
		if !k.start.Valid || !service.OpenAIWeeklyWindowsEquivalent(k.start.Time, start) {
			base = 0
		}
		share := shares[k.id]
		allocated += share
		if _, err := tx.ExecContext(ctx, `UPDATE api_keys SET
			upstream_weekly_usage_percent=$1, upstream_weekly_window_start=$2,
			upstream_weekly_observed_at=$3 WHERE id=$4 AND deleted_at IS NULL`,
			base+share, start, observedAt, k.id); err != nil {
			return err
		}
	}
	if delta > 0 {
		previousUsed = used
		previousCosts = costs
		unattributed += math.Max(0, delta-allocated)
	}
	raw, err = json.Marshal(previousCosts)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO openai_weekly_quota_snapshots
		(account_id,window_start,used_percent,unattributed_percent,costs,observed_at)
		VALUES ($1,$2,$3,$4,$5::jsonb,$6)
		ON CONFLICT (account_id) DO UPDATE SET window_start=EXCLUDED.window_start,
		used_percent=EXCLUDED.used_percent,unattributed_percent=EXCLUDED.unattributed_percent,
		costs=EXCLUDED.costs,observed_at=EXCLUDED.observed_at`,
		accountID, start, previousUsed, unattributed, string(raw), observedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func allocateUpstreamWeeklyDelta(delta float64, previous, current map[int64]float64) map[int64]float64 {
	shares := map[int64]float64{}
	if delta <= 0 || math.IsNaN(delta) || math.IsInf(delta, 0) {
		return shares
	}
	total := 0.0
	for id, cost := range current {
		total += math.Max(0, cost-previous[id])
	}
	if total <= 0 || math.IsNaN(total) || math.IsInf(total, 0) {
		return shares
	}
	for id, cost := range current {
		weight := math.Max(0, cost-previous[id])
		// Match NUMERIC(10,6) without rounding the sum above the observed delta.
		shares[id] = math.Floor(delta*weight/total*1e6) / 1e6
	}
	return shares
}
