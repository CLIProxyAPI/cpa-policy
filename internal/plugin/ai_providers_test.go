package plugin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"cpa-key-billing/internal/billing"
)

func TestAIProviderUsageSumsNamedCredentialsInRange(t *testing.T) {
	app := newAppWithPrice(t, true)
	from := app.store.Now().Add(-time.Hour).Truncate(time.Second)
	to := from.Add(2 * time.Minute)
	for _, sample := range []struct {
		index  string
		at     time.Time
		failed bool
	}{
		{"config-a", from, false},
		{"config-a", from.Add(time.Minute), true},
		{"config-a", from.Add(-time.Minute), false},
		{"config-a", to, false},
		{"config-b", from.Add(time.Minute), false},
		{"auth-file", from.Add(time.Minute), false},
	} {
		record := UsageRecord{Provider: "openai-compatibility", AuthType: "apikey", AuthIndex: sample.index,
			APIKey: accountTestKeyA, Model: "gpt-5.5", RequestedAt: sample.at, Failed: sample.failed,
			Detail: UsageDetail{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 50, TotalTokens: 120}}
		if sample.failed {
			record.Detail = UsageDetail{}
		}
		publishUsageRecord(t, app, record)
	}
	query := url.Values{"from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)},
		"auth_index": {"config-a", " config-b ", "config-a", "config-missing"}}
	var view struct {
		Usage map[string]billing.CredentialUsageStats `json:"usage"`
	}
	callOK(t, app, http.MethodGet, routeAIProviderUsage, query, nil, http.StatusOK, &view)
	if len(view.Usage) != 2 {
		t.Fatalf("usage = %+v, want only the named credentials with requests", view.Usage)
	}
	if got := view.Usage["config-a"]; got.Requests != 2 || got.Failed != 1 || got.InputTokens != 100 || got.OutputTokens != 20 || got.CacheReadTokens != 50 || len(got.Models) != 1 {
		t.Fatalf("config-a usage = %+v", got)
	}
	if got := view.Usage["config-b"]; got.Requests != 1 || got.Failed != 0 {
		t.Fatalf("config-b usage = %+v", got)
	}

	var empty struct {
		Usage map[string]any `json:"usage"`
	}
	callOK(t, app, http.MethodGet, routeAIProviderUsage, nil, nil, http.StatusOK, &empty)
	if empty.Usage == nil || len(empty.Usage) != 0 {
		t.Fatalf("empty selection = %+v", empty)
	}

	tooMany := make([]string, maxAIProviderIndexes+1)
	for i := range tooMany {
		tooMany[i] = "config-" + strconv.Itoa(i)
	}
	for _, invalid := range []url.Values{
		{"auth_index": {" "}}, {"auth_index": {strings.Repeat("a", 513)}}, {"auth_index": tooMany},
		{"auth_index": {"config-a"}, "from": {"bad"}},
		{"auth_index": {"config-a"}, "to": {"bad"}},
	} {
		response := callManagement(t, app, http.MethodGet, routeAIProviderUsage, invalid, nil)
		var body struct {
			Error struct {
				Key string `json:"message_key"`
			} `json:"error"`
		}
		if err := json.Unmarshal(response.Body, &body); err != nil || response.StatusCode != http.StatusBadRequest || body.Error.Key == "" {
			t.Fatalf("invalid query %v: status=%d body=%s", len(invalid["auth_index"]), response.StatusCode, response.Body)
		}
	}
}
