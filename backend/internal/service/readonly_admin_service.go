package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var readonlyEmailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+`)

type readonlyUserReader interface {
	GetByID(ctx context.Context, id int64) (*User, error)
}

type readonlyGroupReader interface {
	GetByID(ctx context.Context, id int64) (*Group, error)
	GetAccountIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error)
}

type readonlyAccountReader interface {
	GetByID(ctx context.Context, id int64) (*Account, error)
	GetByIDs(ctx context.Context, ids []int64) ([]*Account, error)
}

// ReadonlyAdminService owns the complete data contract for scoped readonly
// administrators. It never returns persistence models or credential maps.
type ReadonlyAdminService struct {
	users    readonlyUserReader
	groups   readonlyGroupReader
	accounts readonlyAccountReader
}

func NewReadonlyAdminService(users UserRepository, groups GroupRepository, accounts AccountRepository) *ReadonlyAdminService {
	return newReadonlyAdminService(users, groups, accounts)
}

func newReadonlyAdminService(users readonlyUserReader, groups readonlyGroupReader, accounts readonlyAccountReader) *ReadonlyAdminService {
	return &ReadonlyAdminService{users: users, groups: groups, accounts: accounts}
}

type ReadonlyListFilter struct {
	Search   string
	Platform string
	Status   string
	Type     string
}

type ReadonlyGroupView struct {
	ID                      int64  `json:"id"`
	Name                    string `json:"name"`
	Platform                string `json:"platform"`
	Status                  string `json:"status"`
	SubscriptionType        string `json:"subscription_type"`
	IsExclusive             bool   `json:"is_exclusive"`
	AccountCount            int64  `json:"account_count"`
	ActiveAccountCount      int64  `json:"active_account_count"`
	RateLimitedAccountCount int64  `json:"rate_limited_account_count"`
}

type ReadonlyAccountGroupView struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Status   string `json:"status"`
}

type ReadonlyAccountView struct {
	ID                  int64                      `json:"id"`
	DisplayName         string                     `json:"display_name"`
	Platform            string                     `json:"platform"`
	Type                string                     `json:"type"`
	PlanType            string                     `json:"plan_type,omitempty"`
	MaskedEmail         string                     `json:"masked_email,omitempty"`
	Status              string                     `json:"status"`
	Schedulable         bool                       `json:"schedulable"`
	LastUsedAt          *time.Time                 `json:"last_used_at,omitempty"`
	RateLimited         bool                       `json:"rate_limited"`
	RateLimitResetAt    *time.Time                 `json:"rate_limit_reset_at,omitempty"`
	Codex5HUsedPercent  *float64                   `json:"codex_5h_used_percent,omitempty"`
	Codex5HResetAt      *time.Time                 `json:"codex_5h_reset_at,omitempty"`
	Codex7DUsedPercent  *float64                   `json:"codex_7d_used_percent,omitempty"`
	Codex7DResetAt      *time.Time                 `json:"codex_7d_reset_at,omitempty"`
	CodexUsageUpdatedAt *time.Time                 `json:"codex_usage_updated_at,omitempty"`
	Groups              []ReadonlyAccountGroupView `json:"groups"`
}

type readonlyScope struct {
	groupIDs []int64
	groups   map[int64]*Group
}

func (s *ReadonlyAdminService) loadScope(ctx context.Context, userID int64) (*readonlyScope, error) {
	if s == nil || s.users == nil || s.groups == nil || userID <= 0 {
		return nil, fmt.Errorf("readonly scope unavailable")
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	seen := make(map[int64]struct{}, len(user.AllowedGroups))
	groupIDs := make([]int64, 0, len(user.AllowedGroups))
	groups := make(map[int64]*Group, len(user.AllowedGroups))
	for _, groupID := range user.AllowedGroups {
		if groupID <= 0 {
			continue
		}
		if _, ok := seen[groupID]; ok {
			continue
		}
		seen[groupID] = struct{}{}
		group, getErr := s.groups.GetByID(ctx, groupID)
		if errors.Is(getErr, ErrGroupNotFound) {
			continue
		}
		if getErr != nil {
			return nil, getErr
		}
		if group == nil {
			continue
		}
		groupIDs = append(groupIDs, groupID)
		groups[groupID] = group
	}
	return &readonlyScope{groupIDs: groupIDs, groups: groups}, nil
}

func (s *ReadonlyAdminService) ListGroups(ctx context.Context, userID int64, page, pageSize int, filter ReadonlyListFilter) ([]ReadonlyGroupView, int64, error) {
	scope, err := s.loadScope(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	items := make([]ReadonlyGroupView, 0, len(scope.groupIDs))
	for _, groupID := range scope.groupIDs {
		group := scope.groups[groupID]
		if group == nil || !readonlyGroupMatches(group, filter) {
			continue
		}
		items = append(items, readonlyGroupFromDomain(group))
	}
	sort.Slice(items, func(i, j int) bool {
		left := scope.groups[items[i].ID]
		right := scope.groups[items[j].ID]
		if left != nil && right != nil && left.SortOrder != right.SortOrder {
			return left.SortOrder < right.SortOrder
		}
		return items[i].ID < items[j].ID
	})
	total := int64(len(items))
	return paginateReadonly(items, page, pageSize), total, nil
}

func (s *ReadonlyAdminService) GetGroup(ctx context.Context, userID, groupID int64) (*ReadonlyGroupView, error) {
	scope, err := s.loadScope(ctx, userID)
	if err != nil {
		return nil, err
	}
	group := scope.groups[groupID]
	if group == nil {
		return nil, ErrGroupNotFound
	}
	view := readonlyGroupFromDomain(group)
	return &view, nil
}

func (s *ReadonlyAdminService) ListAccounts(ctx context.Context, userID int64, page, pageSize int, filter ReadonlyListFilter) ([]ReadonlyAccountView, int64, error) {
	scope, err := s.loadScope(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	if len(scope.groupIDs) == 0 {
		return []ReadonlyAccountView{}, 0, nil
	}
	accountIDs, err := s.groups.GetAccountIDsByGroupIDs(ctx, scope.groupIDs)
	if err != nil {
		return nil, 0, err
	}
	accounts, err := s.accounts.GetByIDs(ctx, accountIDs)
	if err != nil {
		return nil, 0, err
	}
	items := make([]ReadonlyAccountView, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		groups := readonlyAuthorizedAccountGroups(account, scope.groups)
		if len(groups) == 0 {
			continue
		}
		view := readonlyAccountFromDomain(account, groups)
		if readonlyAccountMatches(view, filter) {
			items = append(items, view)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	total := int64(len(items))
	return paginateReadonly(items, page, pageSize), total, nil
}

func (s *ReadonlyAdminService) GetAccount(ctx context.Context, userID, accountID int64) (*ReadonlyAccountView, error) {
	scope, err := s.loadScope(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(scope.groupIDs) == 0 {
		return nil, ErrAccountNotFound
	}
	accountIDs, err := s.groups.GetAccountIDsByGroupIDs(ctx, scope.groupIDs)
	if err != nil {
		return nil, err
	}
	authorized := false
	for _, id := range accountIDs {
		if id == accountID {
			authorized = true
			break
		}
	}
	if !authorized {
		return nil, ErrAccountNotFound
	}
	account, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	groups := readonlyAuthorizedAccountGroups(account, scope.groups)
	if len(groups) == 0 {
		return nil, ErrAccountNotFound
	}
	view := readonlyAccountFromDomain(account, groups)
	return &view, nil
}

func readonlyGroupMatches(group *Group, filter ReadonlyListFilter) bool {
	if filter.Platform != "" && group.Platform != filter.Platform {
		return false
	}
	if filter.Status != "" && group.Status != filter.Status {
		return false
	}
	search := strings.ToLower(strings.TrimSpace(filter.Search))
	return search == "" || strings.Contains(strings.ToLower(group.Name), search)
}

func readonlyAccountMatches(account ReadonlyAccountView, filter ReadonlyListFilter) bool {
	if filter.Platform != "" && account.Platform != filter.Platform {
		return false
	}
	if filter.Status != "" && account.Status != filter.Status {
		return false
	}
	if filter.Type != "" && account.Type != filter.Type {
		return false
	}
	search := strings.ToLower(strings.TrimSpace(filter.Search))
	if search == "" {
		return true
	}
	return strings.Contains(strings.ToLower(account.DisplayName), search) ||
		strings.Contains(strings.ToLower(account.MaskedEmail), search) ||
		strings.Contains(strings.ToLower(account.Platform), search) ||
		strings.Contains(strings.ToLower(account.Type), search)
}

func readonlyGroupFromDomain(group *Group) ReadonlyGroupView {
	return ReadonlyGroupView{
		ID:                      group.ID,
		Name:                    group.Name,
		Platform:                group.Platform,
		Status:                  group.Status,
		SubscriptionType:        group.SubscriptionType,
		IsExclusive:             group.IsExclusive,
		AccountCount:            group.AccountCount,
		ActiveAccountCount:      group.ActiveAccountCount,
		RateLimitedAccountCount: group.RateLimitedAccountCount,
	}
}

func readonlyAuthorizedAccountGroups(account *Account, allowed map[int64]*Group) []ReadonlyAccountGroupView {
	if account == nil || len(allowed) == 0 {
		return []ReadonlyAccountGroupView{}
	}
	accountGroupIDs := make(map[int64]struct{}, len(account.GroupIDs)+len(account.AccountGroups)+len(account.Groups))
	for _, groupID := range account.GroupIDs {
		accountGroupIDs[groupID] = struct{}{}
	}
	for _, relation := range account.AccountGroups {
		accountGroupIDs[relation.GroupID] = struct{}{}
	}
	for _, group := range account.Groups {
		if group != nil {
			accountGroupIDs[group.ID] = struct{}{}
		}
	}
	groups := make([]ReadonlyAccountGroupView, 0, len(accountGroupIDs))
	for groupID := range accountGroupIDs {
		group := allowed[groupID]
		if group == nil {
			continue
		}
		groups = append(groups, ReadonlyAccountGroupView{ID: group.ID, Name: group.Name, Platform: group.Platform, Status: group.Status})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	return groups
}

func readonlyAccountFromDomain(account *Account, groups []ReadonlyAccountGroupView) ReadonlyAccountView {
	email := readonlyAccountEmail(account)
	maskedEmail := MaskReadonlyEmail(email)
	rateReset := cloneTimePointer(account.RateLimitResetAt)
	now := time.Now()
	rateLimited := account.RateLimitedAt != nil && (rateReset == nil || rateReset.After(now))
	return ReadonlyAccountView{
		ID:                  account.ID,
		DisplayName:         readonlyAccountDisplayName(account, email, maskedEmail),
		Platform:            account.Platform,
		Type:                account.Type,
		PlanType:            readonlyString(account.Credentials, "plan_type"),
		MaskedEmail:         maskedEmail,
		Status:              account.Status,
		Schedulable:         account.Schedulable,
		LastUsedAt:          cloneTimePointer(account.LastUsedAt),
		RateLimited:         rateLimited,
		RateLimitResetAt:    rateReset,
		Codex5HUsedPercent:  readonlyExtraFloat(account.Extra, "codex_5h_used_percent"),
		Codex5HResetAt:      readonlyExtraTime(account.Extra, "codex_5h_reset_at"),
		Codex7DUsedPercent:  readonlyExtraFloat(account.Extra, "codex_7d_used_percent"),
		Codex7DResetAt:      readonlyExtraTime(account.Extra, "codex_7d_reset_at"),
		CodexUsageUpdatedAt: readonlyExtraTime(account.Extra, "codex_usage_updated_at"),
		Groups:              groups,
	}
}

func readonlyAccountEmail(account *Account) string {
	if account == nil {
		return ""
	}
	if value := readonlyString(account.Credentials, "email"); value != "" {
		return value
	}
	if value := readonlyString(account.Extra, "email"); value != "" {
		return value
	}
	return readonlyString(account.Extra, "email_address")
}

func readonlyString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

// MaskReadonlyEmail reveals at most the first two characters of the local part
// and preserves the domain. Invalid values are omitted rather than echoed.
func MaskReadonlyEmail(email string) string {
	email = strings.TrimSpace(email)
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	local, domain := email[:at], email[at+1:]
	prefixRunes := []rune(local)
	if len(prefixRunes) > 2 {
		prefixRunes = prefixRunes[:2]
	}
	return string(prefixRunes) + "***@" + domain
}

func readonlyAccountDisplayName(account *Account, email, maskedEmail string) string {
	name := strings.TrimSpace(account.Name)
	if email != "" && maskedEmail != "" {
		lowerName := strings.ToLower(name)
		lowerEmail := strings.ToLower(email)
		if index := strings.Index(lowerName, lowerEmail); index >= 0 {
			name = name[:index] + maskedEmail + name[index+len(email):]
		}
	}
	name = readonlyEmailPattern.ReplaceAllStringFunc(name, MaskReadonlyEmail)
	if name == "" {
		return fmt.Sprintf("%s %s #%d", account.Platform, account.Type, account.ID)
	}
	return name
}

func readonlyExtraFloat(extra map[string]any, key string) *float64 {
	if extra == nil {
		return nil
	}
	value, ok := extra[key]
	if !ok || value == nil {
		return nil
	}
	parsed := parseExtraFloat64(value)
	return &parsed
}

func readonlyExtraTime(extra map[string]any, key string) *time.Time {
	if extra == nil {
		return nil
	}
	value, ok := extra[key]
	if !ok || value == nil {
		return nil
	}
	if parsed, ok := value.(time.Time); ok && !parsed.IsZero() {
		parsed = parsed.UTC()
		return &parsed
	}
	parsed := parseExtraTime(value)
	if parsed.IsZero() {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func paginateReadonly[T any](items []T, page, pageSize int) []T {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []T{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}
