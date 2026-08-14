//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type readonlyUserReaderStub struct {
	user *User
	err  error
}

func (s *readonlyUserReaderStub) GetByID(context.Context, int64) (*User, error) {
	return s.user, s.err
}

type readonlyGroupReaderStub struct {
	groups     map[int64]*Group
	accountIDs []int64
	err        error
}

func (s *readonlyGroupReaderStub) GetByID(_ context.Context, id int64) (*Group, error) {
	if s.err != nil {
		return nil, s.err
	}
	group := s.groups[id]
	if group == nil {
		return nil, ErrGroupNotFound
	}
	return group, nil
}

func (s *readonlyGroupReaderStub) GetAccountIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	return append([]int64(nil), s.accountIDs...), s.err
}

type readonlyAccountReaderStub struct {
	accounts map[int64]*Account
	err      error
}

func (s *readonlyAccountReaderStub) GetByID(_ context.Context, id int64) (*Account, error) {
	if s.err != nil {
		return nil, s.err
	}
	account := s.accounts[id]
	if account == nil {
		return nil, ErrAccountNotFound
	}
	return account, nil
}

func (s *readonlyAccountReaderStub) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	if s.err != nil {
		return nil, s.err
	}
	items := make([]*Account, 0, len(ids))
	seen := map[int64]struct{}{}
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if account := s.accounts[id]; account != nil {
			items = append(items, account)
		}
	}
	return items, nil
}

func TestReadonlyAdminService_EmptyScopeReturnsEmptyLists(t *testing.T) {
	svc := newReadonlyAdminService(
		&readonlyUserReaderStub{user: &User{ID: 9, Role: RoleReadonly}},
		&readonlyGroupReaderStub{},
		&readonlyAccountReaderStub{},
	)

	groups, groupTotal, err := svc.ListGroups(context.Background(), 9, 1, 20, ReadonlyListFilter{})
	require.NoError(t, err)
	require.Empty(t, groups)
	require.Zero(t, groupTotal)

	accounts, accountTotal, err := svc.ListAccounts(context.Background(), 9, 1, 20, ReadonlyListFilter{})
	require.NoError(t, err)
	require.Empty(t, accounts)
	require.Zero(t, accountTotal)

	_, err = svc.GetAccount(context.Background(), 9, 101)
	require.ErrorIs(t, err, ErrAccountNotFound)
}

func TestReadonlyAdminService_ScopesAccountsAndRedactsSecrets(t *testing.T) {
	observedAt := time.Date(2026, 8, 14, 3, 0, 0, 0, time.UTC)
	group10 := &Group{ID: 10, Name: "OpenAI Team", Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeStandard, IsExclusive: true, AccountCount: 2, ActiveAccountCount: 1}
	account := &Account{
		ID:          101,
		Name:        "Primary alice.smith@example.com",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		GroupIDs:    []int64{10, 11},
		Credentials: map[string]any{
			"email":              "alice.smith@example.com",
			"plan_type":          "pro",
			"access_token":       "secret-access-token",
			"refresh_token":      "secret-refresh-token",
			"cookie":             "secret-cookie",
			"chatgpt_account_id": "sensitive-account-id",
		},
		Extra: map[string]any{
			"codex_5h_used_percent":  12.5,
			"codex_7d_used_percent":  34.5,
			"codex_usage_updated_at": observedAt.Format(time.RFC3339),
			"arbitrary_secret":       "must-not-leak",
		},
		Proxy: &Proxy{Password: "proxy-password"},
	}
	svc := newReadonlyAdminService(
		&readonlyUserReaderStub{user: &User{ID: 9, Role: RoleReadonly, AllowedGroups: []int64{10, 10}}},
		&readonlyGroupReaderStub{groups: map[int64]*Group{10: group10}, accountIDs: []int64{101, 101}},
		&readonlyAccountReaderStub{accounts: map[int64]*Account{101: account}},
	)

	items, total, err := svc.ListAccounts(context.Background(), 9, 1, 20, ReadonlyListFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 1, total, "shared account IDs must be deduplicated")
	require.Len(t, items, 1)
	require.Equal(t, "al***@example.com", items[0].MaskedEmail)
	require.Equal(t, "Primary al***@example.com", items[0].DisplayName)
	require.Equal(t, "pro", items[0].PlanType)
	require.Len(t, items[0].Groups, 1)
	require.Equal(t, int64(10), items[0].Groups[0].ID)
	require.NotNil(t, items[0].CodexUsageUpdatedAt)

	encoded, err := json.Marshal(items[0])
	require.NoError(t, err)
	body := string(encoded)
	for _, forbidden := range []string{
		"alice.smith@example.com",
		"secret-access-token",
		"secret-refresh-token",
		"secret-cookie",
		"sensitive-account-id",
		"arbitrary_secret",
		"must-not-leak",
		"proxy-password",
		"credentials",
		"extra",
		"api_key",
	} {
		require.NotContains(t, body, forbidden)
	}
}

func TestReadonlyAdminService_UnauthorizedEnumerationReturnsNotFound(t *testing.T) {
	group := &Group{ID: 10, Name: "Allowed", Platform: PlatformOpenAI, Status: StatusActive}
	account := &Account{ID: 101, Platform: PlatformOpenAI, Type: AccountTypeOAuth, GroupIDs: []int64{11}}
	svc := newReadonlyAdminService(
		&readonlyUserReaderStub{user: &User{ID: 9, Role: RoleReadonly, AllowedGroups: []int64{10}}},
		&readonlyGroupReaderStub{groups: map[int64]*Group{10: group}},
		&readonlyAccountReaderStub{accounts: map[int64]*Account{101: account}},
	)

	_, err := svc.GetGroup(context.Background(), 9, 11)
	require.ErrorIs(t, err, ErrGroupNotFound)
	_, err = svc.GetAccount(context.Background(), 9, 101)
	require.ErrorIs(t, err, ErrAccountNotFound)
	_, err = svc.GetAccount(context.Background(), 9, 999)
	require.ErrorIs(t, err, ErrAccountNotFound)
}

func TestReadonlyAdminService_GetAuthorizedAccount(t *testing.T) {
	group := &Group{ID: 10, Name: "Allowed", Platform: PlatformOpenAI, Status: StatusActive}
	account := &Account{ID: 101, Name: "Allowed account", Platform: PlatformOpenAI, Type: AccountTypeOAuth, GroupIDs: []int64{10}}
	svc := newReadonlyAdminService(
		&readonlyUserReaderStub{user: &User{ID: 9, Role: RoleReadonly, AllowedGroups: []int64{10}}},
		&readonlyGroupReaderStub{groups: map[int64]*Group{10: group}, accountIDs: []int64{101}},
		&readonlyAccountReaderStub{accounts: map[int64]*Account{101: account}},
	)

	item, err := svc.GetAccount(context.Background(), 9, 101)
	require.NoError(t, err)
	require.Equal(t, int64(101), item.ID)
}

func TestReadonlyAdminService_SearchDoesNotUseUnredactedEmail(t *testing.T) {
	group := &Group{ID: 10, Name: "Allowed", Platform: PlatformOpenAI, Status: StatusActive}
	account := &Account{
		ID: 101, Name: "Primary alice.smith@example.com", Platform: PlatformOpenAI, Type: AccountTypeOAuth, GroupIDs: []int64{10},
		Credentials: map[string]any{"email": "alice.smith@example.com"},
	}
	svc := newReadonlyAdminService(
		&readonlyUserReaderStub{user: &User{ID: 9, Role: RoleReadonly, AllowedGroups: []int64{10}}},
		&readonlyGroupReaderStub{groups: map[int64]*Group{10: group}, accountIDs: []int64{101}},
		&readonlyAccountReaderStub{accounts: map[int64]*Account{101: account}},
	)

	items, total, err := svc.ListAccounts(context.Background(), 9, 1, 20, ReadonlyListFilter{Search: "alice.smith@example.com"})
	require.NoError(t, err)
	require.Empty(t, items)
	require.Zero(t, total, "hidden values must not be usable as a search oracle")
}

func TestMaskReadonlyEmail(t *testing.T) {
	require.Equal(t, "a***@example.com", MaskReadonlyEmail("a@example.com"))
	require.Equal(t, "ab***@example.com", MaskReadonlyEmail("abcdef@example.com"))
	require.Equal(t, "用户***@example.com", MaskReadonlyEmail("用户名称@example.com"))
	require.Empty(t, MaskReadonlyEmail("not-an-email"))
}
