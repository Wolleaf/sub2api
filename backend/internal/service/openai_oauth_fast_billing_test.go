package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestShouldApplyOpenAIOAuthFastPricing(t *testing.T) {
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	apiKey := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	tests := []struct {
		name    string
		account *Account
		model   string
		tier    string
		want    bool
	}{
		{name: "gpt-6 astra", account: oauth, model: "gpt-6-astra", tier: "priority", want: true},
		{name: "gpt-5.6 sol", account: oauth, model: "gpt-5.6-sol", tier: "priority", want: true},
		{name: "gpt-5.6 terra alias", account: oauth, model: "openai/gpt_5.6_terra_high", tier: " PRIORITY ", want: true},
		{name: "gpt-5.6 luna alias", account: oauth, model: "gpt5.6-luna-openai-compact", tier: "priority", want: true},
		{name: "bare gpt-5.6 alias", account: oauth, model: "gpt-5.6", tier: "priority", want: true},
		{name: "gpt-5.5", account: oauth, model: "gpt-5.5", tier: "priority", want: true},
		{name: "gpt-5.5 pro remains legacy", account: oauth, model: "gpt-5.5-pro", tier: "priority", want: false},
		{name: "gpt-5.4 remains legacy", account: oauth, model: "gpt-5.4", tier: "priority", want: false},
		{name: "api key remains API priority", account: apiKey, model: "gpt-5.6-sol", tier: "priority", want: false},
		{name: "standard unchanged", account: oauth, model: "gpt-5.6-sol", tier: "", want: false},
		{name: "flex unchanged", account: oauth, model: "gpt-5.6-sol", tier: "flex", want: false},
		{name: "nil account", account: nil, model: "gpt-5.6-sol", tier: "priority", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldApplyOpenAIOAuthFastPricing(tt.account, tt.model, tt.tier))
		})
	}
}

func TestOpenAIGatewayServiceRecordUsage_OAuthFastTierMatrix(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		accountType    string
		serviceTier    string
		openAIWSMode   bool
		costMultiplier float64
	}{
		{name: "oauth sol HTTP", model: "gpt-5.6-sol", accountType: AccountTypeOAuth, serviceTier: "priority", costMultiplier: 2.5},
		{name: "oauth terra WebSocket", model: "openai/gpt_5.6_terra_high", accountType: AccountTypeOAuth, serviceTier: "priority", openAIWSMode: true, costMultiplier: 2.5},
		{name: "oauth luna", model: "gpt-5.6-luna-openai-compact", accountType: AccountTypeOAuth, serviceTier: "priority", costMultiplier: 2.5},
		{name: "oauth bare alias", model: "gpt-5.6", accountType: AccountTypeOAuth, serviceTier: "priority", costMultiplier: 2.5},
		{name: "oauth gpt-5.5", model: "gpt-5.5", accountType: AccountTypeOAuth, serviceTier: "priority", costMultiplier: 2.5},
		{name: "api key gpt-5.6", model: "gpt-5.6-sol", accountType: AccountTypeAPIKey, serviceTier: "priority", costMultiplier: 2.0},
		{name: "oauth gpt-5.4", model: "gpt-5.4", accountType: AccountTypeOAuth, serviceTier: "priority", costMultiplier: 2.0},
		{name: "oauth gpt-5.5 pro", model: "gpt-5.5-pro", accountType: AccountTypeOAuth, serviceTier: "priority", costMultiplier: 2.0},
		{name: "oauth standard", model: "gpt-5.6-sol", accountType: AccountTypeOAuth, serviceTier: "", costMultiplier: 1.0},
		{name: "oauth flex", model: "gpt-5.6-sol", accountType: AccountTypeOAuth, serviceTier: "flex", costMultiplier: 0.5},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(
				usageRepo,
				&openAIRecordUsageUserRepoStub{},
				&openAIRecordUsageSubRepoStub{},
				nil,
			)
			serviceTier := tt.serviceTier
			var serviceTierPtr *string
			if serviceTier != "" {
				serviceTierPtr = &serviceTier
			}

			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{
					RequestID:    fmt.Sprintf("resp_oauth_fast_matrix_%d", i),
					Model:        tt.model,
					Usage:        OpenAIUsage{InputTokens: 100, OutputTokens: 50},
					ServiceTier:  serviceTierPtr,
					OpenAIWSMode: tt.openAIWSMode,
					Duration:     time.Second,
				},
				APIKey: &APIKey{ID: int64(1100 + i)},
				User:   &User{ID: int64(2100 + i)},
				Account: &Account{
					ID:       int64(3100 + i),
					Platform: PlatformOpenAI,
					Type:     tt.accountType,
				},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			standard, calcErr := svc.billingService.calculateCostWithServiceTierPolicy(
				tt.model,
				UsageTokens{InputTokens: 100, OutputTokens: 50},
				1.1,
				"",
				false,
			)
			require.NoError(t, calcErr)
			require.InDelta(t, standard.InputCost*tt.costMultiplier, usageRepo.lastLog.InputCost, 1e-12)
			require.InDelta(t, standard.OutputCost*tt.costMultiplier, usageRepo.lastLog.OutputCost, 1e-12)
			require.InDelta(t, standard.TotalCost*tt.costMultiplier, usageRepo.lastLog.TotalCost, 1e-12)
			require.InDelta(t, standard.ActualCost*tt.costMultiplier, usageRepo.lastLog.ActualCost, 1e-12)
			if serviceTierPtr != nil {
				require.NotNil(t, usageRepo.lastLog.ServiceTier)
				require.Equal(t, tt.serviceTier, *usageRepo.lastLog.ServiceTier)
			}
		})
	}
}

func TestOpenAIGatewayServiceRecordUsage_OAuthFastScalesAllTokenComponentsAndLongContext(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(
		usageRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		nil,
	)
	// v0.1.185 keeps long-context tiers in the model catalog instead of the
	// static fallback card, matching the production pricing path.
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1.1
	svc.billingService = NewBillingService(cfg, newStubPricingServiceFromJSON(t, `{
		"gpt-5.6-sol": {
			"litellm_provider": "openai",
			"mode": "chat",
			"input_cost_per_token": 5e-06,
			"output_cost_per_token": 3e-05,
			"cache_read_input_token_cost": 5e-07,
			"cache_creation_input_token_cost": 6.25e-06,
			"long_context_input_token_threshold": 272000,
			"long_context_input_cost_multiplier": 2.0,
			"long_context_output_cost_multiplier": 1.5
		}
	}`))
	serviceTier := "priority"
	usage := OpenAIUsage{
		InputTokens:              300500,
		OutputTokens:             2200,
		CacheCreationInputTokens: 300,
		CacheReadInputTokens:     200,
		ImageInputTokens:         100,
		ImageOutputTokens:        200,
	}
	tokens := UsageTokens{
		InputTokens:         300000,
		OutputTokens:        2200,
		CacheCreationTokens: 300,
		CacheReadTokens:     200,
		ImageInputTokens:    100,
		ImageOutputTokens:   200,
	}

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:   "resp_oauth_fast_all_components",
			Model:       "gpt-5.6-sol",
			Usage:       usage,
			ServiceTier: &serviceTier,
			Duration:    time.Second,
		},
		APIKey: &APIKey{ID: 1201},
		User:   &User{ID: 2201},
		Account: &Account{
			ID:       3201,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{openAILongContextBillingEnabledKey: true},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	standard, calcErr := svc.billingService.calculateCostWithServiceTierPolicy("gpt-5.6-sol", tokens, 1.1, "", true)
	require.NoError(t, calcErr)
	require.True(t, standard.LongContextBillingApplied)
	require.InDelta(t, standard.InputCost*2.5, usageRepo.lastLog.InputCost, 1e-10)
	require.InDelta(t, standard.ImageInputCost*2.5, usageRepo.lastLog.ImageInputCost, 1e-10)
	require.InDelta(t, standard.OutputCost*2.5, usageRepo.lastLog.OutputCost, 1e-10)
	require.InDelta(t, standard.ImageOutputCost*2.5, usageRepo.lastLog.ImageOutputCost, 1e-10)
	require.InDelta(t, standard.CacheCreationCost*2.5, usageRepo.lastLog.CacheCreationCost, 1e-10)
	require.InDelta(t, standard.CacheReadCost*2.5, usageRepo.lastLog.CacheReadCost, 1e-10)
	require.InDelta(t, standard.TotalCost*2.5, usageRepo.lastLog.TotalCost, 1e-10)
	require.InDelta(t, standard.ActualCost*2.5, usageRepo.lastLog.ActualCost, 1e-10)
	require.True(t, usageRepo.lastLog.LongContextBillingApplied)
}

func TestOpenAIGatewayServiceRecordUsage_OAuthFastUsesDynamicStandardPrice(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(
		usageRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		nil,
	)
	svc.billingService = NewBillingService(svc.cfg, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gpt-5.6-sol": {
			InputCostPerToken:                   4e-6,
			InputCostPerTokenPriority:           40e-6,
			OutputCostPerToken:                  13e-6,
			OutputCostPerTokenPriority:          130e-6,
			CacheCreationInputTokenCost:         6e-6,
			CacheCreationInputTokenCostPriority: 60e-6,
			CacheReadInputTokenCost:             0.4e-6,
			CacheReadInputTokenCostPriority:     4e-6,
		},
	}})
	serviceTier := "priority"

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "resp_oauth_fast_dynamic_standard",
			Model:     "gpt-5.6-sol",
			Usage: OpenAIUsage{
				InputTokens:              1000,
				OutputTokens:             500,
				CacheCreationInputTokens: 200,
				CacheReadInputTokens:     100,
			},
			ServiceTier: &serviceTier,
			Duration:    time.Second,
		},
		APIKey:  &APIKey{ID: 1202},
		User:    &User{ID: 2202},
		Account: &Account{ID: 3202, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
	})

	require.NoError(t, err)
	standardTotal := 700*4e-6 + 500*13e-6 + 200*6e-6 + 100*0.4e-6
	require.InDelta(t, standardTotal*2.5, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, standardTotal*2.5*1.1, usageRepo.lastLog.ActualCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_OAuthFastUsesChannelStandardPrice(t *testing.T) {
	const groupID int64 = 77
	inputPrice := 7e-6
	outputPrice := 17e-6
	cacheWritePrice := 9e-6
	cacheReadPrice := 1e-6
	channelCache := newEmptyChannelCache()
	channelCache.pricingByGroupModel[channelModelKey{groupID: groupID, model: "gpt-5.6-sol"}] = &ChannelModelPricing{
		BillingMode:     BillingModeToken,
		InputPrice:      &inputPrice,
		OutputPrice:     &outputPrice,
		CacheWritePrice: &cacheWritePrice,
		CacheReadPrice:  &cacheReadPrice,
	}
	channelCache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	channelCache.groupPlatform[groupID] = ""
	channelCache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(channelCache)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(
		usageRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		nil,
	)
	svc.resolver = NewModelPricingResolver(channelService, svc.billingService)
	serviceTier := "priority"

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "resp_oauth_fast_channel_standard",
			Model:     "gpt-5.6-sol",
			Usage: OpenAIUsage{
				InputTokens:              1000,
				OutputTokens:             500,
				CacheCreationInputTokens: 200,
				CacheReadInputTokens:     100,
			},
			ServiceTier: &serviceTier,
			Duration:    time.Second,
		},
		APIKey: &APIKey{
			ID:      1203,
			GroupID: i64p(groupID),
			Group:   &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 1.3},
		},
		User:    &User{ID: 2203},
		Account: &Account{ID: 3203, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
	})

	require.NoError(t, err)
	standardTotal := 700*inputPrice + 500*outputPrice + 200*cacheWritePrice + 100*cacheReadPrice
	require.InDelta(t, standardTotal*2.5, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, standardTotal*2.5*1.3, usageRepo.lastLog.ActualCost, 1e-12)
}

func TestCalculateOpenAIRecordUsageCost_OAuthFastDoesNotScaleSearchSurcharge(t *testing.T) {
	searchPrice := 10.0 // $10 / 1k searches; 100 calls cost $1.
	svc := &OpenAIGatewayService{billingService: NewBillingService(&config.Config{}, nil)}
	apiKey := &APIKey{Group: &Group{SearchPricePer1k: &searchPrice}}
	billingAccount := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500}

	standard, err := svc.billingService.calculateCostWithServiceTierPolicy("gpt-5.6-sol", tokens, 1, "", false)
	require.NoError(t, err)
	cost, err := svc.calculateOpenAIRecordUsageCost(
		context.Background(),
		&OpenAIForwardResult{SearchCount: 100},
		apiKey,
		billingAccount,
		[]string{"gpt-5.6-sol"},
		1,
		1,
		1,
		1,
		tokens,
		"priority",
		boolPtr(false),
		time.Time{},
	)

	require.NoError(t, err)
	require.InDelta(t, standard.TotalCost*2.5+1, cost.TotalCost, 1e-12)
	require.InDelta(t, standard.ActualCost*2.5+1, cost.ActualCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_OAuthFastUsesCredentialParentForShadow(t *testing.T) {
	tests := []struct {
		name           string
		shadowType     string
		parentType     string
		costMultiplier float64
	}{
		{name: "OAuth parent enables 2.5x", shadowType: AccountTypeAPIKey, parentType: AccountTypeOAuth, costMultiplier: 2.5},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			parentID := int64(4200 + i)
			accountRepo := &openAIRecordUsageAccountRepoStub{account: &Account{
				ID:       parentID,
				Platform: PlatformOpenAI,
				Type:     tt.parentType,
			}}
			svc := newOpenAIRecordUsageServiceForTest(
				usageRepo,
				&openAIRecordUsageUserRepoStub{},
				&openAIRecordUsageSubRepoStub{},
				nil,
			)
			svc.accountRepo = accountRepo
			serviceTier := "priority"

			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{
					RequestID:   fmt.Sprintf("resp_oauth_fast_shadow_%d", i),
					Model:       "gpt-5.6-sol",
					Usage:       OpenAIUsage{InputTokens: 100, OutputTokens: 50},
					ServiceTier: &serviceTier,
					Duration:    time.Second,
				},
				APIKey: &APIKey{ID: int64(1300 + i)},
				User:   &User{ID: int64(2300 + i)},
				Account: &Account{
					ID:              int64(3300 + i),
					Platform:        PlatformOpenAI,
					Type:            tt.shadowType,
					ParentAccountID: &parentID,
					QuotaDimension:  QuotaDimensionSpark,
				},
			})

			require.NoError(t, err)
			require.Equal(t, 1, accountRepo.calls)
			standard, calcErr := svc.billingService.calculateCostWithServiceTierPolicy(
				"gpt-5.6-sol",
				UsageTokens{InputTokens: 100, OutputTokens: 50},
				1.1,
				"",
				false,
			)
			require.NoError(t, calcErr)
			require.InDelta(t, standard.TotalCost*tt.costMultiplier, usageRepo.lastLog.TotalCost, 1e-12)
		})
	}
}
