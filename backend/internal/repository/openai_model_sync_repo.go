package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type openAIModelSyncRepository struct{ db *sql.DB }

func NewOpenAIModelSyncRepository(db *sql.DB) service.OpenAIModelSyncWriter {
	return &openAIModelSyncRepository{db: db}
}

func (r *openAIModelSyncRepository) AppendModels(ctx context.Context, a *service.Account, models []string) (bool, error) {
	if !service.OpenAIModelAutoSyncEligible(a) {
		return false, nil
	}
	additions := make(map[string]string)
	existing := a.Credentials["model_mapping"].(map[string]any)
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || strings.ContainsAny(model, "*\r\n\x00") {
			continue
		}
		if _, ok := existing[model]; !ok {
			additions[model] = model
		}
	}
	if len(additions) == 0 {
		return false, nil
	}
	expected, err := json.Marshal(a.Credentials)
	if err != nil {
		return false, err
	}
	payload, err := json.Marshal(additions)
	if err != nil {
		return false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	// CAS prevents stale discovery from overwriting a concurrent token refresh,
	// whitelist edit or mode change. Other credentials and extra are untouched.
	result, err := tx.ExecContext(ctx, `UPDATE accounts
		SET credentials = jsonb_set(credentials, '{model_mapping}',
			(credentials->'model_mapping') || $1::jsonb), updated_at = NOW()
		WHERE id = $2 AND credentials = $3::jsonb AND deleted_at IS NULL
		AND platform = 'openai' AND type = 'oauth' AND status = 'active'
		AND parent_account_id IS NULL`, string(payload), a.ID, string(expected))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &a.ID, nil, nil); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
