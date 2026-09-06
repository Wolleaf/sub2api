package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type modelSyncAccountStub struct {
	AccountRepository
	account *Account
}

func (r modelSyncAccountStub) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

type modelSyncReaderStub struct {
	calls  int
	err    error
	models []string
}

func (r *modelSyncReaderStub) SyncUpstreamModelCatalog(context.Context, *Account) (*UpstreamModelCatalog, error) {
	r.calls++
	return &UpstreamModelCatalog{Models: r.models}, r.err
}

type modelSyncWriterStub struct {
	calls  int
	models []string
}

func (w *modelSyncWriterStub) AppendModels(_ context.Context, _ *Account, models []string) (bool, error) {
	w.calls++
	w.models = models
	return true, nil
}
func modelSyncTestAccount() *Account {
	return &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"access_token": "test-secret", "model_mapping": map[string]any{"old": "old"}}}
}

func TestOpenAIModelAutoSyncEligibility(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Account)
	}{
		{"api_key", func(a *Account) { a.Type = AccountTypeAPIKey }},
		{"other_platform", func(a *Account) { a.Platform = PlatformAnthropic }},
		{"disabled", func(a *Account) { a.Status = "disabled" }},
		{"shadow", func(a *Account) { id := int64(2); a.ParentAccountID = &id }},
		{"unrestricted", func(a *Account) { delete(a.Credentials, "model_mapping") }},
		{"empty", func(a *Account) { a.Credentials["model_mapping"] = map[string]any{} }},
		{"custom_mapping", func(a *Account) { a.Credentials["model_mapping"] = map[string]any{"alias": "target"} }},
		{"wildcard", func(a *Account) { a.Credentials["model_mapping"] = map[string]any{"gpt-*": "gpt-*"} }},
		{"invalid", func(a *Account) { a.Credentials["model_mapping"] = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := modelSyncTestAccount()
			tc.edit(a)
			r := &modelSyncReaderStub{}
			w := &modelSyncWriterStub{}
			s := &OpenAIModelSyncService{repo: modelSyncAccountStub{account: a}, reader: r, writer: w}
			s.syncAccount(context.Background(), 1)
			require.Zero(t, r.calls)
			require.Zero(t, w.calls)
		})
	}
	require.True(t, OpenAIModelAutoSyncEligible(modelSyncTestAccount()))
}

func TestOpenAIModelAutoSyncAppendAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		models []string
		err    error
		want   []string
	}{
		{"append", []string{"old", " new ", "new", "", "*", "bad\nmodel"}, nil, []string{"new"}},
		{"unchanged", []string{"old"}, nil, nil},
		{"empty", nil, nil, nil},
		{"upstream_error", []string{"new"}, errors.New("sensitive upstream error"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := modelSyncTestAccount()
			r := &modelSyncReaderStub{models: tc.models, err: tc.err}
			w := &modelSyncWriterStub{}
			s := &OpenAIModelSyncService{repo: modelSyncAccountStub{account: a}, reader: r, writer: w}
			s.syncAccount(context.Background(), 1)
			require.Equal(t, tc.want, w.models)
			require.Equal(t, "test-secret", a.Credentials["access_token"])
			require.Equal(t, map[string]any{"old": "old"}, a.Credentials["model_mapping"])
		})
	}
}

type modelSyncBlockingReader struct{ started chan struct{} }

func (r modelSyncBlockingReader) SyncUpstreamModelCatalog(ctx context.Context, _ *Account) (*UpstreamModelCatalog, error) {
	close(r.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestOpenAIModelAutoSyncStartsImmediatelyAndCancels(t *testing.T) {
	r := modelSyncBlockingReader{started: make(chan struct{})}
	s := &OpenAIModelSyncService{repo: modelSyncAccountStub{account: modelSyncTestAccount()}, reader: r, ids: []int64{1}, interval: time.Hour}
	s.Start()
	select {
	case <-r.started:
	case <-time.After(3 * time.Second):
		t.Fatal("startup sync did not run")
	}
	s.Stop()
	s.Stop()
}
