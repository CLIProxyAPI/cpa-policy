package billing

import (
	"net/http"
	"time"
)

type CredentialUsageStats struct {
	Requests        int64 `json:"requests"`
	Failed          int64 `json:"failed"`
	InputTokens     int64 `json:"input_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
}

type QuotaHeaderObservation struct {
	Provider string
	At       time.Time
	Headers  http.Header
}

type CredentialUsage struct {
	Stats CredentialUsageStats
	Quota []QuotaHeaderObservation
}

func (s *Store) CredentialUsage(authIndexes []string, scope string, from, to time.Time) (map[string]CredentialUsage, error) {
	from, to, err := usageTimeRange(from, to, s.Now())
	if err != nil {
		return nil, err
	}
	return withRepository(s, func(repo Repository) (map[string]CredentialUsage, error) {
		return repo.CredentialUsage(authIndexes, scope, from, to)
	})
}
