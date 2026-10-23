package moderation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func blockedPolicy(t *testing.T, strategy string) *Compiled {
	t.Helper()
	p := DefaultPolicy()
	p.Mode = "pre_block"
	p.Strategy = strategy
	p.Rules = []Rule{{ID: "local", Name: "Test rule", Kind: "keyword", Pattern: "blocked-marker"}}
	c, err := p.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func chat(text string) []byte {
	body, _ := json.Marshal(map[string]any{"messages": []any{map[string]string{"role": "user", "content": text}}})
	return body
}

func TestExtractionFormatsAndBoundaries(t *testing.T) {
	fixtures := map[string]string{
		"openai":          `{"messages":[{"role":"system","content":"secret-system"},{"role":"user","content":"old-user"},{"role":"assistant","content":"secret-assistant"},{"role":"user","content":[{"type":"text","text":"latest-user"}]},{"role":"tool","content":"secret-tool"}]}`,
		"claude":          `{"system":"secret-system","messages":[{"role":"user","content":"old-user"},{"role":"user","content":[{"type":"tool_result","content":"secret-tool"},{"type":"text","text":"latest-user"}]}]}`,
		"codex":           `{"instructions":"secret-system","input":[{"role":"user","content":"old-user"},{"type":"function_call_output","output":"secret-tool"},{"type":"message","role":"user","content":[{"type":"input_text","text":"latest-user"}]},{"role":"assistant","content":"secret-assistant"}]}`,
		"openai-response": `{"input":[{"role":"user","content":"old-user"},{"role":"user","content":"latest-user"}]}`,
		"gemini":          `{"systemInstruction":{"parts":[{"text":"secret-system"}]},"contents":[{"role":"user","parts":[{"text":"old-user"}]},{"role":"model","parts":[{"text":"secret-assistant"}]},{"role":"user","parts":[{"functionResponse":{"response":{"text":"secret-tool"}}},{"text":"latest-user"}]}]}`,
	}
	for format, body := range fixtures {
		t.Run(format, func(t *testing.T) {
			if got := Extract(format, []byte(body), "latest_user"); got.Text != "latest-user" || got.Reason != "" {
				t.Fatalf("latest: %+v", got)
			}
			if got := Extract(format, []byte(body), "all_users"); got.Text != "old-user\nlatest-user" || strings.Contains(got.Text, "secret") {
				t.Fatalf("all: %+v", got)
			}
		})
	}
	for _, tc := range []struct{ format, body, reason string }{
		{"openai", `{`, "invalid_body"}, {"unknown", `{}`, "unsupported_format"},
		{"openai", `{"messages":[{"role":"assistant","content":"blocked-marker"}]}`, "no_user_text"},
		{"openai", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"secret-image"}}]}]}`, "media_only"},
	} {
		if got := Extract(tc.format, []byte(tc.body), "latest_user"); got.Reason != tc.reason {
			t.Fatalf("%s: %+v", tc.reason, got)
		}
	}
	if got := Extract("openai", chat(strings.Repeat("文", MaxTextRunes+1)), "latest_user"); got.Reason != "text_too_large" || got.Text != "" {
		t.Fatalf("oversize: %+v", got)
	}
	if got := Extract("openai", make([]byte, MaxBodyBytes+1), "all_users"); got.Reason != "body_too_large" {
		t.Fatal(got.Reason)
	}
	if got := Extract("openai-response", []byte(`{"input":"single input"}`), "latest_user"); got.Text != "single input" {
		t.Fatal(got)
	}
}

func TestModesLocalRulesAndFilters(t *testing.T) {
	e := &Engine{}
	for _, mode := range []string{"off", "observe", "pre_block"} {
		c := blockedPolicy(t, "keywords")
		c.Policy.Mode = mode
		got := e.Check(context.Background(), c, API{}, "demo", "openai", chat("BLOCKED-MARKER"))
		want := map[string]string{"off": "allow", "observe": "observe", "pre_block": "block"}[mode]
		if got.Action != want {
			t.Fatalf("%s: %+v", mode, got)
		}
	}
	c := blockedPolicy(t, "keywords")
	c.Policy.ModelMode, c.Policy.Models = "include", []string{"target"}
	if e.Check(context.Background(), c, API{}, "other", "openai", chat("blocked-marker")).Status != "skipped" {
		t.Fatal("model filter ignored")
	}
	if e.Check(context.Background(), c, API{}, "target", "openai", chat("blocked-marker")).Action != "block" {
		t.Fatal("included model bypassed")
	}
	c.Policy.ModelMode = "exclude"
	if e.Check(context.Background(), c, API{}, "target", "openai", chat("blocked-marker")).Status != "skipped" {
		t.Fatal("exclusion ignored")
	}
	c.Policy.ModelMode = "all"
	for _, action := range []string{"allow", "block"} {
		c.Policy.FailureAction = action
		got := e.Check(context.Background(), c, API{}, "", "unknown", []byte(`{}`))
		if got.Status != "error" || got.Action != action {
			t.Fatal(got)
		}
	}
}

func TestAuditAPIAndRedirection(t *testing.T) {
	var calls atomic.Int32
	var response atomic.Value
	response.Store(`{"results":[{"flagged":true,"categories":{"violence":true},"category_scores":{"violence":0.7}}]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/audit")
			w.WriteHeader(302)
			return
		}
		if r.Header.Get("Authorization") != "Bearer dummy-audit-key" {
			t.Error("missing audit credential")
		}
		var payload map[string]string
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["input"] != "ordinary user text" || payload["model"] != "dummy-audit" {
			t.Error("unexpected audit payload")
		}
		_, _ = w.Write([]byte(response.Load().(string)))
	}))
	defer server.Close()
	t.Setenv("TEST_MODERATION_KEY", "dummy-audit-key")
	api := API{URL: server.URL + "/audit", Model: "dummy-audit", KeyEnv: "TEST_MODERATION_KEY", TimeoutMS: 1000, MaxInFlight: 2}
	e := &Engine{}
	c := blockedPolicy(t, "keywords_and_api")
	if got := e.Check(context.Background(), c, api, "demo", "openai", chat("blocked-marker")); got.Action != "block" || calls.Load() != 0 {
		t.Fatal("local hit called remote", got)
	}
	check := func() Result {
		return e.Check(context.Background(), c, api, "demo", "openai", chat("ordinary user text"))
	}
	if got := check(); got.Action != "block" || len(got.Categories) != 1 {
		t.Fatal(got)
	}
	api.Thresholds = map[string]float64{"violence": 0.8}
	if got := check(); got.Status != "passed" {
		t.Fatalf("configured threshold not honored: %+v", got)
	}
	api.Thresholds["violence"] = 0.6
	if got := check(); got.Action != "block" {
		t.Fatal(got)
	}
	api.Thresholds["missing"] = 0.5
	if got := check(); got.Status != "error" || got.Reason != "api_category_missing" {
		t.Fatal(got)
	}
	api.Thresholds = nil
	for _, raw := range []string{`{}`, `{"results":[]}`, `{"results":[{}]}`, `{"results":[{"flagged":"false"}]}`, `{"results":[{"flagged":false,"categories":{"violence":true}}]}`, `not json`} {
		response.Store(raw)
		if got := check(); got.Status != "error" || got.Action != "block" {
			t.Fatalf("invalid audit result was allowed: %+v", got)
		}
	}
	api.URL = server.URL + "/redirect"
	before := calls.Load()
	if got := check(); got.Reason != "api_http_error" || calls.Load() != before+1 {
		t.Fatal("audit followed redirect", got)
	}
	api.KeyEnv = "UNSET_AUDIT_TEST_KEY"
	t.Setenv(api.KeyEnv, "")
	if got := check(); got.Reason != "api_key_missing" || calls.Load() != before+1 {
		t.Fatal(got)
	}
}

func TestAuditConcurrencyTimeoutAndCancellation(t *testing.T) {
	entered := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = ioCopyDiscard(r)
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	api := API{URL: server.URL, Model: "test", MaxInFlight: 1, TimeoutMS: 1000}
	e := &Engine{}
	c := blockedPolicy(t, "api")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() { done <- e.Check(ctx, c, api, "test", "openai", chat("hello")) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("audit did not start")
	}
	if got := e.Check(context.Background(), c, api, "test", "openai", chat("hello")); got.Reason != "api_busy" {
		t.Fatal(got)
	}
	cancel()
	select {
	case got := <-done:
		if got.Reason != "api_timeout_or_canceled" {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop audit")
	}
	if e.active.Load() != 0 {
		t.Fatal("audit slot leaked")
	}
	api.TimeoutMS = 100
	if got := e.Check(context.Background(), c, api, "test", "openai", chat("hello")); got.Reason != "api_timeout_or_canceled" {
		t.Fatal(got)
	}
}

func ioCopyDiscard(r *http.Request) (int64, error) {
	var body json.RawMessage
	err := json.NewDecoder(r.Body).Decode(&body)
	return int64(len(body)), err
}

func TestConfigValidationAndCopies(t *testing.T) {
	base := DefaultConfig()
	base.Policy = blockedPolicy(t, "keywords").Policy
	for _, change := range []func(*Config){
		func(c *Config) { c.Mode = "bad" }, func(c *Config) { c.Rules[0].Pattern = "" }, func(c *Config) { c.Rules[0].Kind = "regex"; c.Rules[0].Pattern = "[" },
		func(c *Config) { c.Rules[0].Kind = "regex"; c.Rules[0].Pattern = ".*" }, func(c *Config) { c.Rules = append(c.Rules, c.Rules[0]) },
		func(c *Config) { c.API.URL = "http://remote.invalid/audit" }, func(c *Config) { c.API.URL = "https://user:password@example.com/audit" },
		func(c *Config) { c.API.URL = "https://example.com/audit?api_key=secret" }, func(c *Config) { c.API.KeyEnv = "sk-real-secret" },
		func(c *Config) { c.API.Thresholds["violence"] = 2 }, func(c *Config) { c.Strategy = "api" },
	} {
		cfg := base.Clone()
		change(&cfg)
		if _, err := cfg.Normalize(); err == nil {
			t.Fatal("invalid config accepted", cfg.Mode, cfg.Strategy)
		}
	}
	compiled, err := base.Policy.Compile()
	if err != nil {
		t.Fatal(err)
	}
	base.Rules[0].Pattern = "changed"
	if compiled.Policy.Rules[0].Pattern != "blocked-marker" {
		t.Fatal("config aliases compiled rules")
	}
}
