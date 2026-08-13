package repository

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClassifyOpenAIWeeklyResetTopology(t *testing.T) {
	eligible := openAIWeeklyResetTopologyRow{
		groupID:     10,
		accountID:   20,
		platform:    service.PlatformOpenAI,
		accountType: service.AccountTypeOAuth,
		status:      service.StatusActive,
	}

	tests := []struct {
		name       string
		rows       []openAIWeeklyResetTopologyRow
		wantID     int64
		wantReason string
		wantOK     bool
	}{
		{name: "safe single oauth", rows: []openAIWeeklyResetTopologyRow{eligible}, wantID: 20, wantOK: true},
		{name: "multiple mixed accounts", rows: []openAIWeeklyResetTopologyRow{eligible, {accountID: 21, platform: service.PlatformOpenAI, accountType: service.AccountTypeAPIKey, status: service.StatusActive}}, wantReason: "multiple_or_mixed_accounts"},
		{name: "shadow", rows: []openAIWeeklyResetTopologyRow{{accountID: 20, platform: service.PlatformOpenAI, accountType: service.AccountTypeOAuth, status: service.StatusActive, parentAccountID: sql.NullInt64{Int64: 19, Valid: true}}}, wantReason: "shadow_account"},
		{name: "inactive", rows: []openAIWeeklyResetTopologyRow{{accountID: 20, platform: service.PlatformOpenAI, accountType: service.AccountTypeOAuth, status: "disabled"}}, wantReason: "inactive_account"},
		{name: "deleted", rows: []openAIWeeklyResetTopologyRow{{accountID: 20, platform: service.PlatformOpenAI, accountType: service.AccountTypeOAuth, status: service.StatusActive, deletedAt: sql.NullTime{Time: time.Now(), Valid: true}}}, wantReason: "inactive_account"},
		{name: "no account", wantReason: "multiple_or_mixed_accounts"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotReason, gotOK := classifyOpenAIWeeklyResetTopology(tt.rows)
			require.Equal(t, tt.wantID, gotID)
			require.Equal(t, tt.wantReason, gotReason)
			require.Equal(t, tt.wantOK, gotOK)
		})
	}
}

func TestAdjustedWeeklyUsage(t *testing.T) {
	target := time.Date(2026, 8, 13, 3, 33, 24, 0, time.UTC)
	earlier := target.Add(-24 * time.Hour)
	later := target.Add(24 * time.Hour)

	tests := []struct {
		name         string
		current      float64
		currentStart *time.Time
		historyCost  float64
		want         float64
	}{
		{name: "first calibration sums target window", current: 99, historyCost: 9.25, want: 9.25},
		{name: "local earlier subtracts old segment", current: 15, currentStart: &earlier, historyCost: 6, want: 9},
		{name: "local later adds missing segment", current: 4, currentStart: &later, historyCost: 5, want: 9},
		{name: "same window is idempotent", current: 9, currentStart: &target, historyCost: 100, want: 9},
		{name: "negative drift clamps at zero", current: 2, currentStart: &earlier, historyCost: 3, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.InDelta(t, tt.want, adjustedWeeklyUsage(tt.current, tt.currentStart, target, tt.historyCost), 1e-12)
		})
	}
}
