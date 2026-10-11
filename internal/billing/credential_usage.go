package billing

import (
	"net/http"
	"time"
)

const CredentialUsagePeriod = 30 * 24 * time.Hour

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

func (s *Store) CredentialUsage(authIndexes []string, scope string) (map[string]CredentialUsage, error) {
	return withRepository(s, func(repo Repository) (map[string]CredentialUsage, error) {
		return repo.CredentialUsage(authIndexes, scope, s.Now().Add(-CredentialUsagePeriod))
	})
}
