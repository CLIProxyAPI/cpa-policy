package plugin

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"cpa-key-billing/internal/billing"
	"cpa-key-billing/internal/messages"
)

// quotaMeter says which requests on a credential count against a quota
// window; the zero meter counts all of them. Windows without a meter cover
// traffic the plugin cannot tell apart, such as Code Review or an Antigravity
// model group, and report no usage.
type quotaMeter struct {
	models billing.ModelMatch
	// Exclusive windows alone meter their models, so windows over every
	// model leave those models out.
	exclusive bool
}

// codexLimit is one Codex rate limit. The main limit meters every model but
// those an additional limit meters. Only Spark's additional limit is known to
// meter a model; other additional limits, and Code Review, whose reviews never
// pass through CLIProxyAPI, meter traffic the plugin cannot tell apart.
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
	limit := codexLimit{key: name, labelPrefix: name + " "}
	// Codex names the Spark limit after its model or calls it Spark.
	switch strings.NewReplacer(".", "_", "-", "_", " ", "_").Replace(strings.ToLower(name)) {
	case "gpt_5_3_codex_spark", "codex_spark", "spark":
		limit.model = "gpt-5.3-codex-spark"
	}
	return limit
}

func (limit codexLimit) meter() *quotaMeter {
	switch {
	case limit == codexMainLimit:
		return &quotaMeter{}
	case limit.model != "":
		return &quotaMeter{models: billing.ModelMatch{Include: []string{limit.model}}, exclusive: true}
	}
	return nil
}

// claudeUnifiedLimit keys the 5-hour and weekly windows of Claude's unified
// limit, which covers every model and is reported on each request.
const claudeUnifiedLimit = "unified"

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

const (
	// quotaHistoryRange is how far back an auth file's quota periods are listed.
	quotaHistoryRange = 30 * 24 * time.Hour
	// quotaHistoryLookback first reads the reports of one more week, the
	// longest window providers report on requests, so that a period running at
	// the start of a range is usually read whole at once.
	quotaHistoryLookback = 7 * 24 * time.Hour
)

// quotaCredential is the credential whose requests quota windows meter: an
// auth file under the provider its quota belongs to.
type quotaCredential struct {
	authIndex, provider string
}

// quotaWindowHistory is a metered window as the newest report on a
// credential's requests describes it, with the periods all reports divide it into.
type quotaWindowHistory struct {
	row     quotaRow
	periods []billing.QuotaWindowPeriod
}

// quotaWindowHistories reconstructs, main limits first, the periods of each
// metered window that the credential's requests reported from from until now.
// A response reports only the windows of the limit it used, so a window it
// leaves out is unchanged rather than gone.
func (a *App) quotaWindowHistories(credential quotaCredential, from, now time.Time) ([]quotaWindowHistory, error) {
	observations, err := a.store.QuotaHeaderHistory(credential.authIndex, from, now)
	if err != nil {
		return nil, err
	}
	var histories []quotaWindowHistory
	var reports [][]billing.QuotaWindowReport
	for _, observation := range observations {
		if !strings.EqualFold(observation.Provider, credential.provider) {
			continue
		}
		quota := parseQuotaHeaders(observation.Provider, observation.Headers, observation.At).quota(observation.At)
		for _, row := range quota.Quota {
			if row.meter == nil {
				continue
			}
			index := slices.IndexFunc(histories, func(history quotaWindowHistory) bool { return history.row.sameWindow(row) })
			if index < 0 {
				index = len(histories)
				histories, reports = append(histories, quotaWindowHistory{}), append(reports, nil)
			}
			histories[index].row = row
			report := billing.QuotaWindowReport{At: observation.At, UsedPercent: 100 - *row.RemainingPercent, Plan: quota.Plan}
			report.ResetAt, _ = time.Parse(time.RFC3339, row.ResetAt)
			reports[index] = append(reports[index], report)
		}
	}
	for i := range histories {
		length := time.Duration(histories[i].row.windowSeconds) * time.Second
		histories[i].periods = billing.QuotaWindowPeriods(length, reports[i], now)
	}
	slices.SortFunc(histories, func(left, right quotaWindowHistory) int { return compareQuotaWindows(left.row, right.row) })
	return histories, nil
}

// compareQuotaWindows orders the limits over every model before additional
// ones, and each limit's windows from shortest to longest.
func compareQuotaWindows(left, right quotaRow) int {
	rank := func(row quotaRow) int {
		if row.LimitKey == codexMainLimit.key || row.LimitKey == claudeUnifiedLimit {
			return 0
		}
		return 1
	}
	return cmp.Or(cmp.Compare(rank(left), rank(right)), strings.Compare(left.LimitKey, right.LimitKey),
		cmp.Compare(left.windowSeconds, right.windowSeconds))
}

// completeQuotaHistories reconstructs the windows' periods that run after
// from, each whole: when one began before the reports read so far, they are
// read again from its start, so resets before from still place it.
func (a *App) completeQuotaHistories(credential quotaCredential, from, now time.Time) ([]quotaWindowHistory, error) {
	start := from.Add(-quotaHistoryLookback)
	for {
		histories, err := a.quotaWindowHistories(credential, start, now)
		if err != nil {
			return nil, err
		}
		earliest := start
		for _, history := range histories {
			index := slices.IndexFunc(history.periods, func(period billing.QuotaWindowPeriod) bool { return period.EndsAfter(from) })
			if index >= 0 && history.periods[index].Start.Before(earliest) {
				earliest = history.periods[index].Start
			}
		}
		if !earliest.Before(start) {
			return histories, nil
		}
		start = earliest
	}
}

func (row quotaRow) sameWindow(other quotaRow) bool {
	return row.LimitKey == other.LimitKey && row.windowSeconds == other.windowSeconds
}

// currentPeriod is the period a metered row reports on: the period of the
// window's history that ends at the row's reset time, or else the one the
// reset time declares.
func (row quotaRow) currentPeriod(histories []quotaWindowHistory) (billing.QuotaWindowPeriod, bool) {
	resetAt, err := time.Parse(time.RFC3339, row.ResetAt)
	if row.meter == nil || row.windowSeconds <= 0 || err != nil {
		return billing.QuotaWindowPeriod{}, false
	}
	for _, history := range histories {
		if history.row.sameWindow(row) {
			if period, ok := billing.QuotaPeriodEnding(history.periods, resetAt); ok {
				return period, true
			}
		}
	}
	return billing.QuotaWindowPeriod{Start: resetAt.Add(-time.Duration(row.windowSeconds) * time.Second), ResetAt: resetAt}, true
}

// usagePeriod selects the requests a period of the row's window covers,
// through now or the period's end.
func (row quotaRow) usagePeriod(credential quotaCredential, period billing.QuotaWindowPeriod, now time.Time, exclusive []string) billing.QuotaPeriod {
	end := period.End
	if end.IsZero() {
		end = now
		if !period.ResetAt.IsZero() && period.ResetAt.Before(now) {
			end = period.ResetAt
		}
	}
	models := row.meter.models
	if len(models.Include) == 0 {
		models.Exclude = exclusive
	}
	return billing.QuotaPeriod{AuthIndex: credential.authIndex, Provider: credential.provider, Start: period.Start, End: end, Models: models}
}

func exclusiveModels(rows []quotaRow) []string {
	var models []string
	for _, row := range rows {
		if row.meter != nil && row.meter.exclusive {
			models = append(models, row.meter.models.Include...)
		}
	}
	return models
}

// attachQuotaUsage reports what each metered window of quotas consumed in its
// current period. A scope limits usage to one API key.
func (a *App) attachQuotaUsage(scope string, quotas map[quotaCredential]*authQuotaResponse) error {
	now := a.store.Now()
	var periods []billing.QuotaPeriod
	var rows []*quotaRow
	for credential, quota := range quotas {
		// The windows that requests report take their current periods from history.
		from := now
		for _, row := range quota.Quota {
			if resetAt, err := time.Parse(time.RFC3339, row.ResetAt); err == nil && row.LimitKey != "" && row.meter != nil {
				if start := resetAt.Add(-time.Duration(row.windowSeconds) * time.Second); start.Before(from) {
					from = start
				}
			}
		}
		var histories []quotaWindowHistory
		if from.Before(now) {
			var err error
			if histories, err = a.completeQuotaHistories(credential, from, now); err != nil {
				return err
			}
		}
		exclusive := exclusiveModels(quota.Quota)
		for i := range quota.Quota {
			if period, ok := quota.Quota[i].currentPeriod(histories); ok {
				periods = append(periods, quota.Quota[i].usagePeriod(credential, period, now, exclusive))
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

type authFileHistoryResponse struct {
	From    time.Time                    `json:"from"`
	To      time.Time                    `json:"to"`
	Usage   billing.CredentialUsageStats `json:"usage"`
	Windows []quotaWindowView            `json:"windows"`
}

// quotaWindowView lists a window's periods, newest first.
type quotaWindowView struct {
	Label        string            `json:"label"`
	LabelMessage messages.Message  `json:"label_message,omitzero"`
	LabelPrefix  string            `json:"label_prefix,omitempty"`
	Periods      []quotaPeriodView `json:"periods"`
}

type quotaPeriodView struct {
	Start       time.Time                `json:"start"`
	End         time.Time                `json:"end,omitzero"`
	ResetAt     time.Time                `json:"reset_at,omitzero"`
	EndReason   billing.QuotaPeriodEnd   `json:"end_reason,omitempty"`
	UsedPercent float64                  `json:"used_percent"`
	Plans       []string                 `json:"plans,omitempty"`
	Usage       billing.QuotaPeriodUsage `json:"usage"`
}

// readAuthFileHistory lists an auth file's usage over the last 30 days and the
// periods of its metered quota windows that ran in them, each with its whole
// usage, even from before. A scope limits usage to one API key.
func (a *App) readAuthFileHistory(credential quotaCredential, scope string) (authFileHistoryResponse, error) {
	now := a.store.Now()
	response := authFileHistoryResponse{From: now.Add(-quotaHistoryRange), To: now, Windows: []quotaWindowView{}}
	stats, err := a.store.CredentialUsageStats([]string{credential.authIndex}, scope, response.From, now)
	if err != nil {
		return response, err
	}
	response.Usage = stats[credential.authIndex]
	histories, err := a.completeQuotaHistories(credential, response.From, now)
	if err != nil {
		return response, err
	}
	rows := make([]quotaRow, len(histories))
	for i, history := range histories {
		rows[i] = history.row
	}
	exclusive := exclusiveModels(rows)
	var periods []billing.QuotaPeriod
	for _, history := range histories {
		window := quotaWindowView{Label: history.row.Label, LabelMessage: history.row.LabelMessage, LabelPrefix: history.row.LabelPrefix}
		for i := len(history.periods) - 1; i >= 0; i-- {
			period := history.periods[i]
			if !period.EndsAfter(response.From) {
				break
			}
			window.Periods = append(window.Periods, quotaPeriodView{Start: period.Start, End: period.End, ResetAt: period.ResetAt,
				EndReason: period.EndReason, UsedPercent: period.UsedPercent, Plans: period.Plans})
			periods = append(periods, history.row.usagePeriod(credential, period, now, exclusive))
		}
		if len(window.Periods) > 0 {
			response.Windows = append(response.Windows, window)
		}
	}
	usage, err := a.store.QuotaPeriodUsage(scope, periods)
	if err != nil {
		return response, err
	}
	for w := range response.Windows {
		for p := range response.Windows[w].Periods {
			response.Windows[w].Periods[p].Usage, usage = usage[0], usage[1:]
		}
	}
	return response, nil
}
