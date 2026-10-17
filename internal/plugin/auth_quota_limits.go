package plugin

import (
	"strings"
	"time"

	"cpa-key-billing/internal/billing"
)

// quotaMeter says which requests on a credential count against a quota
// window; the zero meter counts all of them. Windows without a meter cover
// traffic the plugin cannot tell apart, such as Code Review or an Antigravity
// model group, and report no usage.
type quotaMeter struct {
	models billing.ModelMatch
	// Exclusive windows alone meter their models, so windows over every
	// model leave those models out.
	exclusive   bool
	usedPercent *billing.QuotaHeaderValue
}

// codexLimit is one Codex rate limit. The main limit meters every model but
// those an additional limit meters; Codex names each additional limit after
// its model. Code Review meters reviews, which never pass through CLIProxyAPI.
type codexLimit struct {
	key, labelPrefix, model string
}

var (
	codexMainLimit       = codexLimit{key: "codex"}
	codexCodeReviewLimit = codexLimit{key: "code-review", labelPrefix: "Code Review "}
)

func codexAdditionalLimit(name string) codexLimit {
	if name == "" {
		return codexLimit{key: "additional", labelPrefix: "Additional "}
	}
	return codexLimit{key: name, labelPrefix: name + " ", model: name}
}

func (limit codexLimit) meter(role string) *quotaMeter {
	switch {
	case limit == codexMainLimit:
		return &quotaMeter{usedPercent: &billing.QuotaHeaderValue{Name: "X-Codex-" + role + "-Used-Percent", Scale: 1}}
	case limit.model != "":
		return &quotaMeter{models: billing.ModelMatch{Include: []string{limit.model}}, exclusive: true}
	}
	return nil
}

// claudeUnifiedLimit keys the 5-hour and weekly windows of Claude's unified
// limit, which covers every model and reports its utilization on each request.
const claudeUnifiedLimit = "unified"

func claudeUnifiedMeter(role string) *quotaMeter {
	return &quotaMeter{usedPercent: &billing.QuotaHeaderValue{Name: "Anthropic-Ratelimit-Unified-" + role + "-Utilization", Scale: 100}}
}

// claudeModelMeter meters a weekly limit Claude keeps for one model family.
func claudeModelMeter(family string) *quotaMeter {
	return &quotaMeter{models: billing.ModelMatch{Include: []string{family}}}
}

// periodSeconds is the length of a period reported by its start and reset.
func periodSeconds(start, resetAt string) int64 {
	from, errFrom := time.Parse(time.RFC3339, strings.TrimSpace(start))
	to, errTo := time.Parse(time.RFC3339, resetAt)
	if errFrom != nil || errTo != nil || !to.After(from) {
		return 0
	}
	return int64(to.Sub(from) / time.Second)
}

// period returns the requests a metered row covers: those made from its
// window's start through the row's observation.
func (row quotaRow) period(authIndex string, fetchedAt time.Time, exclusive []string) (billing.QuotaPeriod, bool) {
	resetAt, err := time.Parse(time.RFC3339, row.ResetAt)
	if row.meter == nil || row.windowSeconds <= 0 || err != nil {
		return billing.QuotaPeriod{}, false
	}
	end := row.ObservedAt
	if end.IsZero() {
		end = fetchedAt
	}
	models := row.meter.models
	if len(models.Include) == 0 {
		models.Exclude = exclusive
	}
	return billing.QuotaPeriod{AuthIndex: authIndex, Start: resetAt.Add(-time.Duration(row.windowSeconds) * time.Second), End: end,
		Models: models, UsedPercent: row.meter.usedPercent}, true
}

// attachQuotaUsage reports what each metered window of quotas, keyed by auth
// index, consumed in its current period. A scope limits usage to one API key.
func (a *App) attachQuotaUsage(scope string, quotas map[string]*authQuotaResponse) error {
	var periods []billing.QuotaPeriod
	var rows []*quotaRow
	for authIndex, quota := range quotas {
		var exclusive []string
		for _, row := range quota.Quota {
			if row.meter != nil && row.meter.exclusive {
				exclusive = append(exclusive, row.meter.models.Include...)
			}
		}
		for i := range quota.Quota {
			if period, ok := quota.Quota[i].period(authIndex, quota.FetchedAt, exclusive); ok {
				periods = append(periods, period)
				rows = append(rows, &quota.Quota[i])
			}
		}
	}
	if len(periods) == 0 {
		return nil
	}
	usage, err := a.store.QuotaPeriodUsage(scope, periods)
	if err != nil {
		return err
	}
	for i := range usage {
		rows[i].Usage = &usage[i]
	}
	return nil
}
