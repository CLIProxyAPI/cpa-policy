package billing

import (
	"net/http"
	"time"
)

type CredentialUsageStats struct {
	Requests        int64   `json:"requests"`
	Failed          int64   `json:"failed"`
	InputTokens     int64   `json:"input_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CostUSD         float64 `json:"cost_usd"`
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

// QuotaPeriod is one period of a provider quota window on a credential: the
// requests on AuthIndex from Start through End whose upstream model matches
// Models count against it.
type QuotaPeriod struct {
	AuthIndex string
	Start     time.Time
	End       time.Time
	Models    ModelMatch
	// UsedPercent names the stored quota header that reported the window's
	// usage on each request. A counter reset in it means the provider restarted
	// the window early, so the period starts at the first request after it.
	UsedPercent *QuotaHeaderValue
}

// QuotaHeaderValue reads a stored quota header; Scale converts it to percent.
type QuotaHeaderValue struct {
	Name  string
	Scale float64
}

// ModelMatch selects upstream models containing one of Include and none of
// Exclude, ignoring case. An empty Include matches every model.
type ModelMatch struct {
	Include []string
	Exclude []string
}

type QuotaPeriodUsage struct {
	Tokens  int64   `json:"tokens"`
	CostUSD float64 `json:"cost_usd"`
}

// QuotaReading is a header value reported by the request made at At.
type QuotaReading struct {
	At    time.Time
	Value float64
}

// Thresholds for a counter reset, shared with CPA-Manager-Plus. Requests that
// run concurrently report usage slightly out of order, so only a drop from
// meaningful usage to almost none, or by at least half and 20 points, counts.
const (
	quotaResetMinimumPriorPercent = 5
	quotaResetNearZeroPercent     = 1
	quotaResetLargeDropPercent    = 20
)

func (s *Store) QuotaPeriodUsage(scope string, periods []QuotaPeriod) ([]QuotaPeriodUsage, error) {
	return withRepository(s, func(repo Repository) ([]QuotaPeriodUsage, error) {
		usage := make([]QuotaPeriodUsage, len(periods))
		for i, period := range periods {
			if period.UsedPercent != nil {
				readings, err := repo.QuotaReadings(period.AuthIndex, period.UsedPercent.Name, period.Start, period.End)
				if err != nil {
					return nil, err
				}
				if reset := lastQuotaCounterReset(readings, period.UsedPercent.Scale); !reset.IsZero() {
					period.Start = reset
				}
			}
			value, err := repo.QuotaPeriodUsage(period, scope)
			if err != nil {
				return nil, err
			}
			usage[i] = value
		}
		return usage, nil
	})
}

// lastQuotaCounterReset returns when the first request after the latest
// counter reset was made, or zero when the readings show none.
func lastQuotaCounterReset(readings []QuotaReading, scale float64) time.Time {
	var reset time.Time
	for i := 1; i < len(readings); i++ {
		previous, current := readings[i-1].Value*scale, readings[i].Value*scale
		if previous >= quotaResetMinimumPriorPercent && current < previous &&
			(current <= quotaResetNearZeroPercent || previous-current >= quotaResetLargeDropPercent && current <= previous/2) {
			reset = readings[i].At
		}
	}
	return reset
}
