package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"cpa-codex-controls/internal/location"
	"gopkg.in/yaml.v3"
)

func TestCredentialConcurrencyLifecycle(t *testing.T) {
	a := newTestApp(t)
	defer a.Shutdown()
	base := testConfigYAML(t, true)
	configure := func(n int) {
		t.Helper()
		extra, _ := yaml.Marshal(map[string]any{"credential_policies": location.CredentialPolicies{"one": {MaxConcurrency: n}, "two": {MaxConcurrency: 1}}})
		_, err := a.HandleMethod(MethodPluginReconfigure, mustMarshal(t, LifecycleRequest{ConfigYAML: append(append([]byte{}, base...), extra...)}))
		if err != nil {
			t.Fatal(err)
		}
	}
	call := func(id, index string) RequestInterceptResponse {
		t.Helper()
		raw, err := a.HandleMethod(MethodRequestInterceptAfter, mustMarshal(t, RequestInterceptRequest{RequestID: id, SourceFormat: "openai", Metadata: map[string]any{MetadataSelectedIndex: index}}))
		if err != nil {
			t.Fatal(err)
		}
		var out RequestInterceptResponse
		decodeResult(t, raw, &out)
		return out
	}
	complete := func(id string) {
		t.Helper()
		_, err := a.HandleMethod(MethodRequestComplete, mustMarshal(t, RequestCompletion{RequestID: id}))
		if err != nil {
			t.Fatal(err)
		}
	}
	configure(0)
	if call("existing", "one").Terminate {
		t.Fatal("unlimited refused")
	}
	configure(1)
	if got := call("new", "one"); got.StatusCode != 429 || got.ResponseHeaders.Get("Retry-After") != "1" {
		t.Fatalf("hot enable: %+v", got)
	}
	if call("existing", "one").Terminate {
		t.Fatal("same attempt counted twice")
	}
	if call("other", "two").Terminate {
		t.Fatal("independent credentials interfere")
	}
	if got := call("existing", "two"); got.StatusCode != 429 {
		t.Fatal("retry should check destination capacity")
	}
	if call("replacement", "one").Terminate {
		t.Fatal("retry failed to release source")
	}
	complete("replacement")
	complete("replacement")
	complete("other")
	configure(3)
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if !call(fmt.Sprint(i), "one").Terminate {
				accepted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 3 {
		t.Fatalf("admitted %d, want 3", accepted.Load())
	}
	for i := 0; i < 64; i++ {
		complete(fmt.Sprint(i))
	}
	if len(a.upstreamRequests) != 0 || len(a.upstreamCounts) != 0 {
		t.Fatal("completion leaked slots")
	}
	// Completion during any host callback must win over the final admission commit.
	marker := a.beginAdmission("race")
	complete("race")
	got := a.admitUpstream(RequestInterceptRequest{RequestID: "race", Metadata: map[string]any{MetadataSelectedIndex: "one"}}, marker)
	a.endAdmission("race", marker)
	if !got.Terminate || len(a.upstreamRequests) != 0 {
		t.Fatal("completion lost race to admission")
	}
}

func TestCredentialLocationPrecedenceAndReload(t *testing.T) {
	a := newTestApp(t)
	defer a.Shutdown()
	base := string(testConfigYAML(t, true))
	tokyo := location.Location{Country: "JP", Region: "Tokyo", City: "Tokyo", Timezone: "Asia/Tokyo"}
	configure := func(mode string, global bool) {
		t.Helper()
		extra, _ := yaml.Marshal(map[string]any{"credential_policies": location.CredentialPolicies{"one": {LocationMode: mode, Location: tokyo}}})
		if global {
			extra = append(extra, []byte(locationYAML)...)
		}
		_, err := a.HandleMethod(MethodPluginReconfigure, mustMarshal(t, LifecycleRequest{ConfigYAML: append([]byte(base), extra...)}))
		if err != nil {
			t.Fatal(err)
		}
	}
	body := []byte(`{"input":[{"role":"user","content":[{"type":"input_text","text":"<environment_context><timezone>UTC</timezone><current_date>2000-01-01</current_date></environment_context>"}]}]}`)
	req := RequestInterceptRequest{SourceFormat: "codex", Body: body, Metadata: map[string]any{MetadataSelectedIndex: "one"}}
	for _, tt := range []struct {
		mode, zone string
		global     bool
	}{
		{"custom", "Asia/Tokyo", true}, {"custom", "Asia/Tokyo", false},
		{"inherit", "America/Los_Angeles", true}, {"inherit", "", false},
		{"preserve", "", true}, {"preserve", "", false},
	} {
		configure(tt.mode, tt.global)
		out := a.applyRequestLocation(req)
		if out.Terminate {
			t.Fatalf("%s failed", tt.mode)
		}
		if tt.zone == "" {
			if len(out.Body) != 0 {
				t.Fatal("preserve changed body")
			}
		} else if !bytes.Contains(out.Body, []byte(tt.zone)) {
			t.Fatalf("%s: %s", tt.mode, out.Body)
		}
	}
	// Snapshots cannot be mutated by management consumers.
	p := a.store.CredentialPolicies()
	delete(p, "one")
	if len(a.store.CredentialPolicies()) != 1 {
		t.Fatal("mutable policy snapshot")
	}
	before := a.store.CredentialPolicies()
	_, err := a.HandleMethod(MethodPluginReconfigure, mustMarshal(t, LifecycleRequest{ConfigYAML: []byte(base + "\ncredential_policies:\n  one:\n    max_concurrency: -1\n")}))
	if err == nil {
		t.Fatal("invalid config accepted")
	}
	b, _ := json.Marshal(before)
	after, _ := json.Marshal(a.store.CredentialPolicies())
	if !bytes.Equal(b, after) {
		t.Fatal("invalid reload altered policy")
	}
}
