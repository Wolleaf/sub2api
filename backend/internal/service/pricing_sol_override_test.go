package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSolDeploymentOverrideCorrectsStaleCatalogAndKeepsLadder(t *testing.T) {
	override := filepath.Join("..", "..", "..", "deploy", "pricing-overrides.json")
	_, err := os.Stat(override)
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Pricing.OverrideFile = override
	p := &PricingService{cfg: cfg}
	data, err := p.parsePricingData([]byte(gpt56LadderCatalogJSON))
	require.NoError(t, err)
	p.pricingData = data
	billing := NewBillingService(cfg, p)
	standard, err := billing.CalculateCost("gpt-5.6-sol", UsageTokens{InputTokens: 100000, CacheReadTokens: 200000, OutputTokens: 1000}, 1)
	require.NoError(t, err)
	require.InDelta(t, .8+.16+.03, standard.ActualCost, 1e-10)
	require.True(t, standard.LongContextBillingApplied)
	// Other model prices remain from the supplied catalog.
	require.InDelta(t, 2e-6, data["gpt-5.6-terra"].InputCostPerToken, 1e-12)
}
