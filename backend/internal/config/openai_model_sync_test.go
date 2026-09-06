package config

import (
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestOpenAIModelSyncAccountScope(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		ids     []int64
		invalid bool
	}{
		{"", nil, false}, {"1, 2,1", []int64{1, 2}, false}, {"0", nil, true}, {"-1", nil, true}, {"1,", nil, true}, {"secret", nil, true},
	} {
		ids, err := (OpenAIModelSyncConfig{AccountIDs: tc.raw}).IDs()
		if tc.invalid {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, tc.ids, ids)
		}
	}
}

func TestOpenAIModelSyncEnvironment(t *testing.T) {
	// Exercise the actual registered defaults used by Config.Load.
	t.Setenv("OPENAI_MODEL_SYNC_ACCOUNT_IDS", "1,2")
	t.Setenv("OPENAI_MODEL_SYNC_INTERVAL_MINUTES", "15")
	viper.Reset()
	t.Cleanup(viper.Reset)
	setDefaults()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	var cfg Config
	require.NoError(t, viper.Unmarshal(&cfg))
	ids, err := cfg.OpenAIModelSync.IDs()
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, ids)
	require.Equal(t, 15, cfg.OpenAIModelSync.IntervalMinutes)
}
