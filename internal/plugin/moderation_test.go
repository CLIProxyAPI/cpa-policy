package plugin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cpa-codex-controls/internal/location"
	"cpa-codex-controls/internal/moderation"
	"cpa-codex-controls/internal/settings"
	"gopkg.in/yaml.v3"
)

func auditConfig(t *testing.T) settings.Config {
	t.Helper()
	cfg := settings.DefaultConfig()
	cfg.Enabled = true
	cfg.DiagnosticsFile = filepath.Join(t.TempDir(), "diagnostics.db")
	cfg.Moderation.Mode = "pre_block"
	cfg.Moderation.Rules = []moderation.Rule{{ID: "test-rule", Name: "Test rule", Kind: "keyword", Pattern: "block-this"}}
	return cfg
}
func configureAudit(t *testing.T, a *App, cfg settings.Config) {
	t.Helper()
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.HandleMethod(MethodPluginReconfigure, mustMarshal(t, LifecycleRequest{ConfigYAML: raw}))
	if err != nil {
		t.Fatal(err)
	}
}
func callAudit(t *testing.T, a *App, req RequestInterceptRequest) RequestInterceptResponse {
	t.Helper()
	raw, err := a.HandleMethod(MethodRequestInterceptAfter, mustMarshal(t, req))
	if err != nil {
		t.Fatal(err)
	}
	var response RequestInterceptResponse
	decodeResult(t, raw, &response)
	return response
}

func TestModerationAccountPrecedenceAndAdmission(t *testing.T) {
	a := NewApp()
	defer a.Shutdown()
	cfg := auditConfig(t)
	custom := cfg.Moderation.Policy.Clone()
	custom.Rules[0].Pattern = "account-marker"
	cfg.CredentialPolicies = location.CredentialPolicies{
		"custom": {MaxConcurrency: 1, Moderation: &moderation.Override{Mode: "custom", Policy: &custom}},
		"off":    {MaxConcurrency: 1, Moderation: &moderation.Override{Mode: "off"}},
		"global": {MaxConcurrency: 1},
	}
	configureAudit(t, a, cfg)
	req := RequestInterceptRequest{RequestID: "request", SourceFormat: "openai", RequestedModel: "requested", Model: "upstream", Metadata: map[string]any{MetadataSelectedIndex: "global"}, Body: []byte(`{"messages":[{"role":"user","content":"block-this"}]}`)}
	if got := callAudit(t, a, req); got.StatusCode != 403 || !bytes.Contains(got.ResponseBody, []byte("content_policy_violation")) {
		t.Fatal(got)
	}
	if len(a.upstreamCounts) != 0 || len(a.upstreamQueues) != 0 {
		t.Fatal("blocked request consumed concurrency")
	}
	req.Metadata[MetadataSelectedIndex] = "off"
	if got := callAudit(t, a, req); got.Terminate {
		t.Fatal("account off did not override global", got)
	}
	// A retry that moves to a blocked account releases the previous slot.
	req.Metadata[MetadataSelectedIndex] = "global"
	if got := callAudit(t, a, req); got.StatusCode != 403 {
		t.Fatal(got)
	}
	if len(a.upstreamCounts) != 0 {
		t.Fatal("retry leaked previous credential slot")
	}
	req.Metadata[MetadataSelectedIndex] = "custom"
	if got := callAudit(t, a, req); got.Terminate {
		t.Fatal("custom policy incorrectly merged global rules", got)
	}
	req.Body = []byte(`{"messages":[{"role":"user","content":"account-marker"}]}`)
	if got := callAudit(t, a, req); got.StatusCode != 403 {
		t.Fatal(got)
	}
	cfg.Moderation.Mode = "off"
	configureAudit(t, a, cfg)
	if got := callAudit(t, a, req); got.StatusCode != 403 {
		t.Fatal("global off bypassed custom policy")
	}
	req.Metadata[MetadataSelectedIndex] = ""
	if got := callAudit(t, a, req); got.StatusCode != 503 {
		t.Fatal("missing account identity allowed request")
	}
	cfg.Enabled = false
	configureAudit(t, a, cfg)
	if got := callAudit(t, a, req); got.Terminate {
		t.Fatal("plugin disabled still auditing")
	}
}

func TestModerationTimezoneCompositionAndErrorFormats(t *testing.T) {
	a := NewApp()
	defer a.Shutdown()
	cfg := auditConfig(t)
	cfg.CustomLocation = location.Settings{Enabled: true, Location: location.Location{Country: "JP", Region: "Tokyo", City: "Tokyo", Timezone: "Asia/Tokyo"}}
	configureAudit(t, a, cfg)
	body := []byte(`{"input":[{"role":"user","content":[{"type":"input_text","text":"<environment_context><timezone>UTC</timezone><current_date>2000-01-01</current_date></environment_context>"}]}]}`)
	req := RequestInterceptRequest{RequestID: "zone", SourceFormat: "codex", Metadata: map[string]any{MetadataSelectedIndex: "one"}, Body: body}
	if got := callAudit(t, a, req); got.Terminate || !bytes.Contains(got.Body, []byte("Asia/Tokyo")) {
		t.Fatal("audit lost timezone rewrite", got)
	}
	for _, tc := range []struct{ format, body, marker string }{
		{"claude", `{"messages":[{"role":"user","content":"block-this"}]}`, `"permission_error"`},
		{"gemini", `{"contents":[{"role":"user","parts":[{"text":"block-this"}]}]}`, `"PERMISSION_DENIED"`},
		{"openai-response", `{"input":"block-this"}`, `"content_policy_violation"`},
	} {
		req.SourceFormat, req.Body = tc.format, []byte(tc.body)
		if got := callAudit(t, a, req); got.StatusCode != 403 || !bytes.Contains(got.ResponseBody, []byte(tc.marker)) {
			t.Fatalf("%s: %s", tc.format, got.ResponseBody)
		}
	}
}

func TestModerationRecordsPrivacyAndDryRun(t *testing.T) {
	a := NewApp()
	defer a.Shutdown()
	cfg := auditConfig(t)
	cfg.Moderation.LogExcerpt = true
	configureAudit(t, a, cfg)
	text := "block-this Authorization: Bearer dummy-token user@example.com +1 415 555 0123 sk-dummy-long-key custom-secret"
	body, _ := json.Marshal(map[string]any{"messages": []any{map[string]string{"role": "user", "content": text}}})
	req := RequestInterceptRequest{RequestID: "private", SourceFormat: "openai", Headers: http.Header{"Authorization": {"Bearer custom-secret"}}, Metadata: map[string]any{MetadataSelectedIndex: "one"}, Body: body}
	callAudit(t, a, req)
	page, err := a.store.ReadPage("moderation", "", 0, 50)
	if err != nil || len(page.Entries) != 1 {
		t.Fatal(page, err)
	}
	for _, secret := range []string{"dummy-token", "user@example.com", "415 555", "sk-dummy", "custom-secret", "messages"} {
		if bytes.Contains(page.Entries[0], []byte(secret)) {
			t.Fatal("unredacted risk record", secret)
		}
	}
	var event settings.ModerationEvent
	_ = json.Unmarshal(page.Entries[0], &event)
	if event.Action != "block" || !strings.Contains(event.Excerpt, "[redacted]") || event.RuleNames[0] != "Test rule" {
		t.Fatal(event)
	}
	for _, kind := range []string{"errors"} {
		p, _ := a.store.ReadPage(kind, "", 0, 50)
		if len(p.Entries) != 0 {
			t.Fatal("local block fabricated an upstream error")
		}
	}
	testBody := mustMarshal(t, map[string]any{"config": cfg.Moderation, "text": "block-this"})
	if response := a.testModeration(ManagementRequest{Body: testBody}); response.StatusCode != 200 || !bytes.Contains(response.Body, []byte(`"flagged"`)) {
		t.Fatal(response)
	}
	page, _ = a.store.ReadPage("moderation", "", 0, 50)
	if len(page.Entries) != 1 {
		t.Fatal("dry run saved a risk event")
	}
	cfg.Moderation.LogExcerpt = false
	configureAudit(t, a, cfg)
	callAudit(t, a, req)
	page, _ = a.store.ReadPage("moderation", "", 0, 1)
	if bytes.Contains(page.Entries[0], []byte(`"excerpt"`)) {
		t.Fatal("default event stored input")
	}
	// Snapshots must not allow a management reader to mutate compiled policies.
	copy := a.store.ModerationConfig()
	copy.Rules[0].Pattern = "changed"
	if a.store.ModerationConfig().Rules[0].Pattern != "block-this" {
		t.Fatal("mutable global policy")
	}
}

func TestModerationCancellationDoesNotAdmit(t *testing.T) {
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	a := NewApp()
	defer a.Shutdown()
	cfg := auditConfig(t)
	cfg.Moderation.Strategy = "api"
	cfg.Moderation.API.URL = server.URL
	cfg.Moderation.API.KeyEnv = ""
	cfg.Moderation.API.TimeoutMS = 3000
	configureAudit(t, a, cfg)
	req := RequestInterceptRequest{RequestID: "canceled", SourceFormat: "openai", Metadata: map[string]any{MetadataSelectedIndex: "one"}, Body: []byte(`{"messages":[{"role":"user","content":"hello"}]}`)}
	done := make(chan RequestInterceptResponse, 1)
	go func() { done <- callAudit(t, a, req) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("audit did not start")
	}
	_, err := a.HandleMethod(MethodRequestComplete, mustMarshal(t, RequestCompletion{RequestID: req.RequestID}))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.StatusCode != 503 {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("completion did not cancel audit")
	}
	if len(a.upstreamCounts) != 0 || len(a.admissions) != 0 {
		t.Fatal("canceled audit leaked admission")
	}
}

func TestModerationUntrustedLabelsCannotEchoCredentials(t *testing.T) {
	t.Setenv("TEST_AUDIT_LABEL_KEY", "dummy-audit-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"flagged": true, "categories": map[string]bool{"dummy-audit-secret": true, "dummy-client-secret": true}}}})
	}))
	defer server.Close()
	a := NewApp()
	defer a.Shutdown()
	cfg := auditConfig(t)
	cfg.Moderation.Strategy = "api"
	cfg.Moderation.API.URL = server.URL
	cfg.Moderation.API.KeyEnv = "TEST_AUDIT_LABEL_KEY"
	configureAudit(t, a, cfg)
	req := RequestInterceptRequest{RequestID: "labels", SourceFormat: "openai", Model: "dummy-client-secret", Headers: http.Header{"Authorization": {"Bearer dummy-client-secret"}}, Metadata: map[string]any{MetadataSelectedIndex: "one"}, Body: []byte(`{"messages":[{"role":"user","content":"hello"}]}`)}
	if got := callAudit(t, a, req); got.StatusCode != 403 {
		t.Fatal(got)
	}
	page, err := a.store.ReadPage("moderation", "", 0, 10)
	if err != nil || len(page.Entries) != 1 {
		t.Fatal(page, err)
	}
	test := a.testModeration(ManagementRequest{Headers: req.Headers, Body: []byte(`{"text":"hello"}`)})
	for _, raw := range [][]byte{page.Entries[0], test.Body} {
		if bytes.Contains(raw, []byte("dummy-audit-secret")) || bytes.Contains(raw, []byte("dummy-client-secret")) {
			t.Fatal("audit labels exposed credentials")
		}
	}
}
