//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIModelSyncAtomicAppend(t *testing.T) {
	ctx := context.Background()
	credentials := map[string]any{"access_token": "test-token", "model_mapping": map[string]any{"old": "old"}}
	encoded, err := json.Marshal(credentials)
	require.NoError(t, err)
	var id int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,credentials,extra) VALUES($1,'openai','oauth','active',$2,'{"other":true}') RETURNING id`, fmt.Sprintf("model-sync-%d", time.Now().UnixNano()), string(encoded)).Scan(&id))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id=$1`, id)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, id)
	})
	a := &service.Account{ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Credentials: credentials}
	r := NewOpenAIModelSyncRepository(integrationDB)
	// Existing database triggers may initialize extra fields on insert.
	// Compare against the persisted baseline, not the insert literal.
	var extraBefore []byte
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT extra FROM accounts WHERE id=$1`, id).Scan(&extraBefore))
	changed, err := r.AppendModels(ctx, a, []string{"new", "new", "old"})
	require.NoError(t, err)
	require.True(t, changed)
	var actual, extra []byte
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT credentials,extra FROM accounts WHERE id=$1`, id).Scan(&actual, &extra))
	require.JSONEq(t, `{"access_token":"test-token","model_mapping":{"old":"old","new":"new"}}`, string(actual))
	require.JSONEq(t, string(extraBefore), string(extra))
	var events int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_outbox WHERE account_id=$1 AND event_type=$2`, id, service.SchedulerOutboxEventAccountChanged).Scan(&events))
	require.Equal(t, 1, events)
	// Same stale observation is harmless; a fresh no-op also emits no event.
	changed, err = r.AppendModels(ctx, a, []string{"new"})
	require.NoError(t, err)
	require.False(t, changed)
	require.NoError(t, json.Unmarshal(actual, &a.Credentials))
	changed, err = r.AppendModels(ctx, a, []string{"new"})
	require.NoError(t, err)
	require.False(t, changed)
	// Simulate an administrator/token refresh finishing while discovery is in flight.
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{access_token}','"rotated-token"') WHERE id=$1`, id)
	require.NoError(t, err)
	changed, err = r.AppendModels(ctx, a, []string{"later"})
	require.NoError(t, err)
	require.False(t, changed)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT credentials FROM accounts WHERE id=$1`, id).Scan(&actual))
	require.JSONEq(t, `{"access_token":"rotated-token","model_mapping":{"old":"old","new":"new"}}`, string(actual))
	// Even with fresh credentials, a disabled account cannot be updated.
	require.NoError(t, json.Unmarshal(actual, &a.Credentials))
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET status='disabled' WHERE id=$1`, id)
	require.NoError(t, err)
	changed, err = r.AppendModels(ctx, a, []string{"later"})
	require.NoError(t, err)
	require.False(t, changed)
}
