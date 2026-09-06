package config

import (
	"fmt"
	"strconv"
	"strings"
)

type OpenAIModelSyncConfig struct {
	AccountIDs      string `mapstructure:"account_ids"`
	IntervalMinutes int    `mapstructure:"interval_minutes"`
}

func (c OpenAIModelSyncConfig) IDs() ([]int64, error) {
	if strings.TrimSpace(c.AccountIDs) == "" {
		return nil, nil
	}
	var ids []int64
	seen := make(map[int64]bool)
	for _, part := range strings.Split(c.AccountIDs, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("openai_model_sync.account_ids must contain positive comma-separated account IDs")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, nil
}
