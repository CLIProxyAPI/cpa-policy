package plugin

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"cpa-key-billing/internal/billing"
)

func TestUsageQuotaPersistsPartialWindowsAndScopedStatistics(t *testing.T) {
	app, path := newAppWithPriceAndState(t, true)
	at := app.store.Now().Add(-time.Hour).Truncate(time.Second)
	headers := http.Header{
		"x-codex-plan-type":            {"plus"},
		"x-codex-primary-used-percent": {"20"}, "x-codex-primary-window-minutes": {"300"},
		"x-codex-primary-reset-after-seconds": {"1800"},
		"x-codex-secondary-used-percent":      {"40"}, "x-codex-secondary-window-minutes": {"10080"},
		"x-codex-secondary-reset-after-seconds": {"86400"},
		"Set-Cookie":                            {"dummy-private-cookie"}, "Authorization": {"Bearer dummy-private-token"},
	}
	record := UsageRecord{Provider: "codex", AuthType: "oauth", AuthIndex: "credential-1",
		APIKey: "sk-dummy-one", Model: "gpt-5.5", RequestedAt: at, ResponseHeaders: headers,
		Detail: UsageDetail{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 50, TotalTokens: 120}}
	publishUsageRecord(t, app, record)
	record.RequestedAt = at.Add(time.Minute)
	record.APIKey = "sk-dummy-two"
	record.ResponseHeaders = http.Header{
		"X-Codex-Primary-Used-Percent": {"35"}, "X-Codex-Primary-Window-Minutes": {"300"},
		"X-Codex-Primary-Reset-After-Seconds": {"1740"},
	}
	record.Failed = true
	publishUsageRecord(t, app, record)
	record.RequestedAt = at.Add(-time.Minute)
	record.ResponseHeaders = headers
	publishUsageRecord(t, app, record)
	record.RequestedAt = at.Add(2 * time.Minute)
	record.ResponseHeaders = nil
	publishUsageRecord(t, app, record)
	record.RequestedAt = at.Add(-45 * 24 * time.Hour)
	publishUsageRecord(t, app, record)
	app.Shutdown()
	app = newTestApp(t)
	defer app.Shutdown()
	_, err := app.HandleMethod(MethodPluginRegister, mustMarshal(t, LifecycleRequest{
		ConfigYAML: []byte("enabled: true\nstate_file: " + path + "\n"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	app.SetHostCaller(func(method string, _ any) (json.RawMessage, error) {
		if method != hostAuthList {
			t.Fatalf("unexpected host query: %s", method)
		}
		return json.RawMessage(`{"files":[{"auth_index":"credential-1","name":"dummy.json","type":"codex"}]}`), nil
	})
	files, err := app.listAuthFiles(viewAccess{})
	if err != nil {
		t.Fatal(err)
	}
	quota := files[0].Quota
	if files[0].Usage.Requests != 4 || files[0].Usage.Failed != 3 ||
		files[0].Usage.InputTokens != 400 || files[0].Usage.CacheReadTokens != 200 || files[0].Usage.OutputTokens != 80 ||
		quota == nil || len(quota.Quota) != 2 || quota.Plan != "Plus" {
		t.Fatalf("credential view: %+v, quota: %+v", files[0], quota)
	}
	if *quota.Quota[0].RemainingPercent != 65 || *quota.Quota[1].RemainingPercent != 60 ||
		!quota.Quota[0].ObservedAt.Equal(at.Add(time.Minute)) || !quota.Quota[1].ObservedAt.Equal(at) ||
		quota.Quota[0].ResetAt != at.Add(30*time.Minute).UTC().Format(time.RFC3339) {
		t.Fatalf("partial or late quota report changed windows: %+v", quota.Quota)
	}
	files, err = app.listAuthFiles(viewAccess{APIKey: true, Scope: billing.CallerScope("sk-dummy-one")})
	if err != nil || files[0].Usage.Requests != 1 || files[0].Usage.Failed != 0 || files[0].Quota == nil {
		t.Fatalf("scoped view: %+v, err: %v", files, err)
	}
	app.Shutdown()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"dummy-private-cookie", "dummy-private-token", "sk-dummy-one", "sk-dummy-two"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("persisted %s", secret)
		}
	}
}

func TestQuotaHeadersAcceptOnlyProviderQuotaFields(t *testing.T) {
	at := time.Now().UTC()
	claude := UsageRecord{Provider: "claude", AuthType: "oauth", AuthIndex: "dummy", RequestedAt: at,
		ResponseHeaders: http.Header{
			"anthropic-ratelimit-unified-5h-utilization": {"0.23"},
			"anthropic-ratelimit-unified-5h-reset":       {"1800000000"},
			"anthropic-ratelimit-unified-7d-utilization": {"NaN"},
			"x-ratelimit-remaining-tokens":               {"1000"},
		}}
	filtered := usageQuotaHeaders(claude, at)
	decoded := parseQuotaHeaders("claude", filtered, at).quota(at)
	if len(filtered) != 2 || len(decoded.Quota) != 1 || *decoded.Quota[0].RemainingPercent != 77 {
		t.Fatalf("Claude quota: %+v, headers: %v", decoded, filtered)
	}
	claude.AuthType = "apikey"
	if len(usageQuotaHeaders(claude, at)) != 0 {
		t.Fatal("accepted API key credential")
	}
	sample := parseQuotaHeaders("codex", http.Header{
		"X-Codex-Active-Limit":         {"codex_spark"},
		"X-Codex-Primary-Used-Percent": {"90"}, "X-Codex-Primary-Window-Minutes": {"300"},
		"X-Codex-Spark-Limit-Name":           {"Spark"},
		"X-Codex-Spark-Primary-Used-Percent": {"20"}, "X-Codex-Spark-Primary-Window-Minutes": {"300"},
	}, at)
	decoded, filtered = sample.quota(at), sample.headers()
	if len(decoded.Quota) != 1 || decoded.Quota[0].LabelPrefix != "Spark " || filtered.Get("X-Codex-Primary-Used-Percent") != "" {
		t.Fatalf("additional quota presented as main quota: %+v", decoded)
	}
}

func TestCodexQuotaPreservesAdditionalWindowsAndPartialCredits(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Second)
	record := UsageRecord{Provider: "codex", AuthType: "oauth", AuthIndex: "dummy", RequestedAt: at,
		ResponseHeaders: http.Header{
			"X-Codex-Additional-GPT-5.3-Codex-Spark-Limit-Name":             {"GPT-5.3-Codex-Spark"},
			"X-Codex-Additional-GPT-5.3-Codex-Spark-Primary-Used-Percent":   {"20"},
			"X-Codex-Additional-GPT-5.3-Codex-Spark-Primary-Window-Minutes": {"300"},
			"X-Codex-Code-Review-Primary-Used-Percent":                      {"10"},
			"X-Codex-Code-Review-Primary-Window-Minutes":                    {"10080"},
			"X-Codex-Credits-Balance":                                       {"126.4"}, "X-Codex-Credits-Unlimited": {"true"},
		}}
	first := usageQuotaHeaders(record, at)
	record.RequestedAt = at.Add(time.Minute)
	record.ResponseHeaders = http.Header{"X-Codex-Credits-Unlimited": {"false"}}
	quota := observedAuthQuota("codex", "dummy", []billing.QuotaHeaderObservation{
		{Provider: "codex", At: at, Headers: first},
		{Provider: "codex", At: record.RequestedAt, Headers: usageQuotaHeaders(record, at)},
	})
	if quota == nil || len(quota.Quota) != 2 || quota.CreditBalance != "126.4" || quota.CreditsUnlimited ||
		!quota.CreditBalanceObservedAt.Equal(at) || !quota.CreditsUnlimitedObservedAt.Equal(record.RequestedAt) {
		t.Fatalf("quota windows or partial credit reports lost: %+v", quota)
	}
	if quota.Quota[0].Label != "GPT-5.3-Codex-Spark 5-hour limit" || quota.Quota[1].Label != "Code Review Weekly limit" {
		t.Fatalf("quota window labels: %+v", quota.Quota)
	}
}
