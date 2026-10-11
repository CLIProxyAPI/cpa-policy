package plugin

import (
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"cpa-key-billing/internal/billing"
)

var codexQuotaGroup = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,120}$`)

type quotaHeaderSample struct {
	provider  string
	plan      string
	balance   string
	unlimited *bool
	windows   []quotaHeaderWindow
}

type quotaHeaderWindow struct {
	prefix      string
	labelPrefix string
	role        string
	seconds     int64
	usedPercent float64
	resetAt     int64
}

func usageQuotaHeaders(record UsageRecord, fallback time.Time) http.Header {
	if !strings.EqualFold(strings.TrimSpace(record.AuthType), "oauth") || strings.TrimSpace(record.AuthIndex) == "" {
		return nil
	}
	at := record.RequestedAt
	if at.IsZero() {
		at = fallback
	}
	return parseQuotaHeaders(record.Provider, record.ResponseHeaders, at).headers()
}

func parseQuotaHeaders(provider string, input http.Header, at time.Time) quotaHeaderSample {
	headers := make(http.Header)
	for key, values := range input {
		if len(values) > 0 && len(values[0]) <= 256 {
			headers.Set(strings.TrimSpace(key), strings.TrimSpace(values[0]))
		}
	}
	sample := quotaHeaderSample{provider: strings.ToLower(strings.TrimSpace(provider))}
	switch sample.provider {
	case "codex":
		groups := []string{}
		for key := range headers {
			if strings.HasPrefix(key, "X-Codex-") && strings.HasSuffix(key, "-Limit-Name") {
				group := strings.TrimSuffix(strings.TrimPrefix(key, "X-Codex-"), "-Limit-Name")
				if codexQuotaGroup.MatchString(group) && headers.Get(key) != "" {
					groups = append(groups, group)
				}
			}
		}
		sort.Strings(groups)
		active := quotaLimitAlias(headers.Get("X-Codex-Active-Limit"))
		additionalActive := false
		for _, group := range groups {
			additionalActive = additionalActive || active != "" && active == quotaLimitAlias(group)
		}
		if !additionalActive {
			sample.appendCodexWindows(headers, "X-Codex-", "", at)
		}
		for _, group := range groups {
			prefix := "X-Codex-" + group + "-"
			sample.appendCodexWindows(headers, prefix, headers.Get(prefix+"Limit-Name")+" ", at)
		}
		if plan := headers.Get("X-Codex-Plan-Type"); len(plan) <= 40 && codexQuotaGroup.MatchString(plan) {
			sample.plan = plan
		}
		sample.appendCodexWindows(headers, "X-Codex-Code-Review-", "Code Review ", at)
		sample.balance = codexCreditBalance(headers.Get("X-Codex-Credits-Balance"))
		if value := headers.Get("X-Codex-Credits-Unlimited"); value == "true" || value == "false" {
			unlimited := value == "true"
			sample.unlimited = &unlimited
		}
	case "claude":
		for _, window := range []struct {
			role    string
			seconds int64
		}{{"5h", 5 * 60 * 60}, {"7d", 7 * 24 * 60 * 60}} {
			prefix := "Anthropic-Ratelimit-Unified-"
			used, ok := finiteQuotaHeader(headers, prefix+window.role+"-Utilization")
			if !ok || used < 0 || math.IsInf(used*100, 0) {
				continue
			}
			sample.windows = append(sample.windows, quotaHeaderWindow{prefix: prefix, role: window.role,
				seconds: window.seconds, usedPercent: used * 100, resetAt: quotaHeaderReset(headers, prefix+window.role+"-", "Reset", at)})
		}
	}
	return sample
}

func (s *quotaHeaderSample) appendCodexWindows(headers http.Header, prefix, labelPrefix string, at time.Time) {
	for _, role := range []string{"Primary", "Secondary"} {
		key := prefix + role + "-"
		used, ok := finiteQuotaHeader(headers, key+"Used-Percent")
		minutes, err := strconv.ParseInt(headers.Get(key+"Window-Minutes"), 10, 64)
		if !ok || used < 0 || err != nil || minutes <= 0 || minutes > 366*24*60 {
			continue
		}
		s.windows = append(s.windows, quotaHeaderWindow{prefix: prefix, labelPrefix: labelPrefix,
			role: role, seconds: minutes * 60, usedPercent: used, resetAt: quotaHeaderReset(headers, key, "Reset-At", at)})
	}
}

func (s quotaHeaderSample) headers() http.Header {
	result := make(http.Header)
	if s.plan != "" {
		result.Set("X-Codex-Plan-Type", s.plan)
	}
	if s.balance != "" {
		result.Set("X-Codex-Credits-Balance", s.balance)
	}
	if s.unlimited != nil {
		result.Set("X-Codex-Credits-Unlimited", strconv.FormatBool(*s.unlimited))
	}
	for _, window := range s.windows {
		prefix := window.prefix + window.role + "-"
		usedKey, resetKey, used := "Used-Percent", "Reset-At", window.usedPercent
		if s.provider == "claude" {
			usedKey, resetKey, used = "Utilization", "Reset", used/100
		} else {
			result.Set(prefix+"Window-Minutes", strconv.FormatInt(window.seconds/60, 10))
			if window.labelPrefix != "" && window.prefix != "X-Codex-Code-Review-" {
				result.Set(window.prefix+"Limit-Name", strings.TrimSpace(window.labelPrefix))
			}
		}
		result.Set(prefix+usedKey, strconv.FormatFloat(used, 'f', -1, 64))
		if window.resetAt > 0 {
			result.Set(prefix+resetKey, strconv.FormatInt(window.resetAt, 10))
		}
	}
	return result
}

func (s quotaHeaderSample) quota(at time.Time) authQuotaResponse {
	result := authQuotaResponse{FetchedAt: at, Plan: codexPlanLabel(s.plan), Quota: []quotaRow{}}
	if s.balance != "" {
		result.CreditBalance, result.CreditBalanceObservedAt = s.balance, at
	}
	if s.unlimited != nil {
		result.CreditsUnlimited, result.CreditsUnlimitedObservedAt = *s.unlimited, at
	}
	for start := 0; start < len(s.windows); {
		end := start
		windows := []quotaWindow{}
		for end < len(s.windows) && s.windows[end].prefix == s.windows[start].prefix {
			window := s.windows[end]
			value := quotaWindow{Role: window.role, Seconds: window.seconds, RemainingPercent: remainingPercent(100 - window.usedPercent)}
			if window.resetAt > 0 {
				value.ResetAt = time.Unix(window.resetAt, 0).UTC().Format(time.RFC3339)
			}
			windows = append(windows, value)
			end++
		}
		appendQuotaWindows(&result, s.windows[start].labelPrefix, windows)
		start = end
	}
	for i := range result.Quota {
		result.Quota[i].ObservedAt = at
	}
	return result
}

func quotaHeaderReset(headers http.Header, prefix, resetKey string, at time.Time) int64 {
	if reset, err := strconv.ParseInt(headers.Get(prefix+resetKey), 10, 64); err == nil && reset > 0 && reset <= 253402300799 {
		return reset
	}
	if resetKey == "Reset-At" {
		if after, err := strconv.ParseInt(headers.Get(prefix+"Reset-After-Seconds"), 10, 64); err == nil && after >= 0 && after <= 366*24*60*60 {
			return at.Add(time.Duration(after) * time.Second).Unix()
		}
	}
	return 0
}

func finiteQuotaHeader(headers http.Header, key string) (float64, bool) {
	value, err := strconv.ParseFloat(headers.Get(key), 64)
	return value, err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func quotaLimitAlias(value string) string {
	value = strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(value))
	return strings.TrimPrefix(value, "codex")
}

func observedAuthQuota(category, revision string, observations []billing.QuotaHeaderObservation) *authQuotaResponse {
	var result *authQuotaResponse
	positions := make(map[string]int)
	for _, observation := range observations {
		if !strings.EqualFold(observation.Provider, category) {
			continue
		}
		sample := parseQuotaHeaders(category, observation.Headers, observation.At)
		if sample.plan == "" && sample.balance == "" && sample.unlimited == nil && len(sample.windows) == 0 {
			continue
		}
		if result == nil {
			result = &authQuotaResponse{AuthRevision: revision, Quota: []quotaRow{}}
		}
		decoded := sample.quota(observation.At)
		result.FetchedAt = observation.At
		if decoded.Plan != "" {
			result.Plan, result.PlanObservedAt = decoded.Plan, observation.At
		}
		if !decoded.CreditBalanceObservedAt.IsZero() {
			result.CreditBalance, result.CreditBalanceObservedAt = decoded.CreditBalance, observation.At
		}
		if !decoded.CreditsUnlimitedObservedAt.IsZero() {
			result.CreditsUnlimited, result.CreditsUnlimitedObservedAt = decoded.CreditsUnlimited, observation.At
		}
		for _, row := range decoded.Quota {
			if i, exists := positions[row.Label]; exists {
				result.Quota[i] = row
			} else {
				positions[row.Label] = len(result.Quota)
				result.Quota = append(result.Quota, row)
			}
		}
	}
	return result
}
