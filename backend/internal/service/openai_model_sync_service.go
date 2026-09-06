package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// OpenAIModelSyncWriter atomically appends models only if the credentials have
// not changed since discovery. Implementations also publish scheduler changes.
type OpenAIModelSyncWriter interface {
	AppendModels(context.Context, *Account, []string) (bool, error)
}

type openAIModelCatalogReader interface {
	SyncUpstreamModelCatalog(context.Context, *Account) (*UpstreamModelCatalog, error)
}

// OpenAIModelSyncService is explicitly scoped by deployment account IDs. Empty
// scope disables it. It never turns unrestricted accounts into whitelists or
// changes custom routing mappings.
type OpenAIModelSyncService struct {
	repo     AccountRepository
	reader   openAIModelCatalogReader
	writer   OpenAIModelSyncWriter
	ids      []int64
	interval time.Duration
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func ProvideOpenAIModelSyncService(repo AccountRepository, reader *AccountTestService, writer OpenAIModelSyncWriter, cfg *config.Config) *OpenAIModelSyncService {
	ids, _ := cfg.OpenAIModelSync.IDs() // Config.Validate rejects invalid IDs.
	s := &OpenAIModelSyncService{repo: repo, reader: reader, writer: writer, ids: ids, interval: time.Duration(cfg.OpenAIModelSync.IntervalMinutes) * time.Minute}
	s.Start()
	return s
}

func (s *OpenAIModelSyncService) Start() {
	if len(s.ids) == 0 || s.interval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runCycle(ctx)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runCycle(ctx)
			}
		}
	}()
}

func (s *OpenAIModelSyncService) Stop() {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

func (s *OpenAIModelSyncService) runCycle(ctx context.Context) {
	for _, id := range s.ids {
		if ctx.Err() != nil {
			return
		}
		attempt, cancel := context.WithTimeout(ctx, 60*time.Second)
		s.syncAccount(attempt, id)
		cancel()
	}
}

func (s *OpenAIModelSyncService) syncAccount(ctx context.Context, id int64) {
	account, err := s.repo.GetByID(ctx, id)
	if err != nil {
		slog.Warn("openai_model_auto_sync_failed", "account_id", id, "stage", "load")
		return
	}
	if !OpenAIModelAutoSyncEligible(account) {
		slog.Warn("openai_model_auto_sync_skipped", "account_id", id, "reason", "requires_active_oauth_with_explicit_identity_whitelist")
		return
	}
	catalog, err := s.reader.SyncUpstreamModelCatalog(ctx, account)
	if err != nil || catalog == nil || len(catalog.Models) == 0 {
		// Never log upstream payloads, errors containing credentials, or tokens.
		slog.Warn("openai_model_auto_sync_failed", "account_id", id, "stage", "fetch")
		return
	}
	models := make([]string, 0, len(catalog.Models))
	seen := make(map[string]bool)
	existing, ok := account.Credentials["model_mapping"].(map[string]any)
	if !ok {
		return
	}
	for _, model := range catalog.Models {
		model = strings.TrimSpace(model)
		if model == "" || strings.ContainsAny(model, "*\r\n\x00") || seen[model] {
			continue
		}
		seen[model] = true
		if _, ok := existing[model]; !ok {
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		slog.Info("openai_model_auto_sync_ok", "account_id", id, "added", 0, "upstream_count", len(catalog.Models))
		return
	}
	changed, err := s.writer.AppendModels(ctx, account, models)
	if err != nil {
		slog.Warn("openai_model_auto_sync_failed", "account_id", id, "stage", "save")
		return
	}
	if !changed {
		slog.Info("openai_model_auto_sync_deferred", "account_id", id, "reason", "account_changed_during_fetch")
		return
	}
	slog.Info("openai_model_auto_sync_ok", "account_id", id, "added", len(models), "upstream_count", len(catalog.Models))
}

func OpenAIModelAutoSyncEligible(a *Account) bool {
	if a == nil || a.Platform != PlatformOpenAI || a.Type != AccountTypeOAuth || a.Status != StatusActive || a.IsShadow() {
		return false
	}
	mapping, ok := a.Credentials["model_mapping"].(map[string]any)
	if !ok || len(mapping) == 0 {
		return false
	}
	for from, to := range mapping {
		target, ok := to.(string)
		if !ok || from == "" || from != target || strings.Contains(from, "*") {
			return false
		}
	}
	return true
}
