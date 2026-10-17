package plugin

import (
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"cpa-key-billing/internal/billing"
)

func quotaUsageByLabel(t *testing.T, quota *authQuotaResponse) map[string]*billing.QuotaPeriodUsage {
	t.Helper()
	if quota == nil {
		t.Fatal("no quota")
	}
	usage := make(map[string]*billing.QuotaPeriodUsage)
	for _, row := range quota.Quota {
		usage[row.Label] = row.Usage
	}
	return usage
}

func TestObservedQuotaUsageStartsAfterEarlyResetAndSkipsAdditionalLimitModels(t *testing.T) {
	app, _ := newAppWithPriceAndState(t, true)
	base := app.store.Now().Add(-3 * time.Hour).Truncate(time.Second)
	unix := func(at time.Time) string { return strconv.FormatInt(at.Unix(), 10) }
	fiveHourReset, weeklyReset := base.Add(4*time.Hour), base.Add(5*24*time.Hour)
	codex := func(primary string, primaryReset time.Time, secondary string) http.Header {
		return http.Header{
			"X-Codex-Primary-Used-Percent": {primary}, "X-Codex-Primary-Window-Minutes": {"300"}, "X-Codex-Primary-Reset-At": {unix(primaryReset)},
			"X-Codex-Secondary-Used-Percent": {secondary}, "X-Codex-Secondary-Window-Minutes": {"10080"}, "X-Codex-Secondary-Reset-At": {unix(weeklyReset)},
		}
	}
	for _, event := range []struct {
		offset  time.Duration
		model   string
		apiKey  string
		tokens  int64
		headers http.Header
	}{
		{-2 * time.Hour, "gpt-5.5", "sk-dummy-one", 1000, codex("90", base.Add(-time.Hour), "10")},
		{0, "gpt-5.5", "sk-dummy-one", 100, codex("40", fiveHourReset, "20")},
		{10 * time.Minute, "gpt-5.5", "sk-dummy-one", 200, codex("85", fiveHourReset, "30")},
		// The provider reset the counter early but kept the window's reset time.
		{20 * time.Minute, "gpt-5.5", "sk-dummy-one", 400, codex("0", fiveHourReset, "31")},
		{30 * time.Minute, "gpt-5.5", "sk-dummy-one", 800, nil},
		{40 * time.Minute, "gpt-5.3-codex-spark", "sk-dummy-one", 1600, http.Header{
			"X-Codex-Active-Limit": {"codex_bengalfox"}, "X-Codex-Bengalfox-Limit-Name": {"GPT-5.3-Codex-Spark"},
			"X-Codex-Bengalfox-Primary-Used-Percent": {"10"}, "X-Codex-Bengalfox-Primary-Window-Minutes": {"300"},
			"X-Codex-Bengalfox-Primary-Reset-At": {unix(base.Add(5 * time.Hour))},
		}},
		{50 * time.Minute, "gpt-5.5", "sk-dummy-two", 3200, codex("3", fiveHourReset, "32")},
	} {
		publishUsageRecord(t, app, UsageRecord{Provider: "codex", AuthType: "oauth", AuthIndex: "codex-1", APIKey: event.apiKey,
			Model: event.model, RequestedAt: base.Add(event.offset), ResponseHeaders: event.headers,
			Detail: UsageDetail{InputTokens: event.tokens, TotalTokens: event.tokens}})
	}
	app.SetHostCaller(func(string, any) (json.RawMessage, error) {
		return json.RawMessage(`{"files":[{"auth_index":"codex-1","name":"dummy.json","type":"codex"}]}`), nil
	})
	files, err := app.listAuthFiles(viewAccess{}, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	usage := quotaUsageByLabel(t, files[0].Quota)
	if fiveHour := usage["5-hour limit"]; fiveHour == nil || fiveHour.Tokens != 4400 || math.Abs(fiveHour.CostUSD-0.0044) > 1e-12 {
		t.Fatalf("5-hour usage = %+v", fiveHour)
	}
	if weekly := usage["Weekly limit"]; weekly == nil || weekly.Tokens != 5700 {
		t.Fatalf("weekly usage = %+v", weekly)
	}
	if spark := usage["GPT-5.3-Codex-Spark 5-hour limit"]; spark == nil || spark.Tokens != 1600 {
		t.Fatalf("spark usage = %+v", spark)
	}
	files, err = app.listAuthFiles(viewAccess{APIKey: true, Tracked: true, Scope: billing.CallerScope("sk-dummy-one")}, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if fiveHour := quotaUsageByLabel(t, files[0].Quota)["5-hour limit"]; fiveHour == nil || fiveHour.Tokens != 1200 {
		t.Fatalf("scoped 5-hour usage = %+v", fiveHour)
	}
}

func TestFetchedQuotaUsageCoversModelFamilyWindowsUpToTheFetch(t *testing.T) {
	app, _ := newAppWithPriceAndState(t, true)
	now := app.store.Now()
	for _, event := range []struct {
		offset time.Duration
		model  string
		tokens int64
	}{
		{-6 * time.Hour, "claude-opus-4-1", 400},
		{-time.Hour, "claude-opus-4-1", 100},
		{-30 * time.Minute, "claude-sonnet-4-5", 200},
	} {
		publishUsageRecord(t, app, UsageRecord{Provider: "claude", AuthType: "oauth", AuthIndex: "claude-1", APIKey: "sk-dummy",
			Model: event.model, RequestedAt: now.Add(event.offset), Detail: UsageDetail{InputTokens: event.tokens, TotalTokens: event.tokens}})
	}
	resetAt := func(after time.Duration) string { return now.Add(after).UTC().Format(time.RFC3339) }
	usage := `{"five_hour":{"utilization":30,"resets_at":"` + resetAt(time.Hour) + `"},` +
		`"seven_day_opus":{"utilization":10,"resets_at":"` + resetAt(24*time.Hour) + `"},` +
		`"seven_day_oauth_apps":{"utilization":5,"resets_at":"` + resetAt(24*time.Hour) + `"}}`
	app.SetHostCaller(func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case hostAuthList:
			return json.RawMessage(`{"files":[{"auth_index":"claude-1","name":"claude.json","type":"claude"}]}`), nil
		case hostAuthGet:
			return json.RawMessage(`{"json":{"access_token":"dummy-token"}}`), nil
		}
		if payload.(hostHTTPRequest).URL != "https://api.anthropic.com/api/oauth/usage" {
			return mustJSONRaw(t, hostHTTPResponse{StatusCode: http.StatusNotFound}), nil
		}
		return mustJSONRaw(t, hostHTTPResponse{StatusCode: http.StatusOK, Body: []byte(usage)}), nil
	})
	response := app.authQuota(ManagementRequest{Query: url.Values{"auth_index": {"claude-1"}}}, viewAccess{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.StatusCode, response.Body)
	}
	var result struct {
		Quota []struct {
			Label string                    `json:"label"`
			Usage *billing.QuotaPeriodUsage `json:"usage"`
		} `json:"quota"`
	}
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"5-hour limit": 300, "Opus weekly limit": 500, "OAuth Apps weekly limit": -1}
	for _, row := range result.Quota {
		tokens, ok := want[row.Label]
		if !ok || tokens < 0 && row.Usage != nil || tokens >= 0 && (row.Usage == nil || row.Usage.Tokens != tokens) {
			t.Fatalf("%s usage = %+v", row.Label, row.Usage)
		}
		delete(want, row.Label)
	}
	if len(want) != 0 {
		t.Fatalf("missing rows: %v", want)
	}
}
