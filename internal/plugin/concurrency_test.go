package plugin

import (
	"bytes"
	"testing"
	"time"

	"cpa-codex-controls/internal/location"
	"cpa-codex-controls/internal/settings"
	"gopkg.in/yaml.v3"
)

type concurrencyHarness struct {
	t    *testing.T
	app  *App
	base []byte
}

func newConcurrencyHarness(t *testing.T) *concurrencyHarness {
	a := newTestApp(t)
	t.Cleanup(a.Shutdown)
	return &concurrencyHarness{t: t, app: a, base: testConfigYAML(t, true)}
}
func (h *concurrencyHarness) config(capacity, seconds int, policies location.CredentialPolicies) {
	h.t.Helper()
	h.configure(map[string]any{"concurrency": settings.ConcurrencySettings{AccountQueueCapacity: capacity, QueueTimeoutSeconds: seconds}, "credential_policies": policies})
}
func (h *concurrencyHarness) configure(config map[string]any) {
	h.t.Helper()
	extra, err := yaml.Marshal(config)
	if err != nil {
		h.t.Fatal(err)
	}
	_, err = h.app.HandleMethod(MethodPluginReconfigure, mustMarshal(h.t, LifecycleRequest{ConfigYAML: append(append([]byte{}, h.base...), extra...)}))
	if err != nil {
		h.t.Fatal(err)
	}
}
func (h *concurrencyHarness) call(id, index string) RequestInterceptResponse {
	h.t.Helper()
	raw, err := h.app.HandleMethod(MethodRequestInterceptAfter, mustMarshal(h.t, RequestInterceptRequest{RequestID: id, ToFormat: "codex", SourceFormat: "openai", Metadata: map[string]any{MetadataSelectedIndex: index}}))
	if err != nil {
		h.t.Error(err)
		return RequestInterceptResponse{Terminate: true}
	}
	var response RequestInterceptResponse
	decodeResult(h.t, raw, &response)
	return response
}
func (h *concurrencyHarness) complete(id string) {
	h.t.Helper()
	if _, err := h.app.HandleMethod(MethodRequestComplete, mustMarshal(h.t, RequestCompletion{RequestID: id})); err != nil {
		h.t.Fatal(err)
	}
}
func (h *concurrencyHarness) async(id, index string) <-chan RequestInterceptResponse {
	result := make(chan RequestInterceptResponse, 1)
	go func() { result <- h.call(id, index) }()
	return result
}
func (h *concurrencyHarness) waiting(index string, count int) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.app.admissionsMu.Lock()
		n := len(h.app.upstreamQueues[index])
		h.app.admissionsMu.Unlock()
		if n == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	h.t.Fatalf("queue %s did not reach %d", index, count)
}
func receiveAdmission(t *testing.T, ch <-chan RequestInterceptResponse) RequestInterceptResponse {
	t.Helper()
	select {
	case response := <-ch:
		return response
	case <-time.After(4 * time.Second):
		t.Fatal("admission did not finish")
		return RequestInterceptResponse{}
	}
}
func assertConcurrencyError(t *testing.T, response RequestInterceptResponse, code string) {
	t.Helper()
	if response.StatusCode != 429 || !response.Terminate || response.ResponseHeaders.Get("Retry-After") != "1" || !bytes.Contains(response.ResponseBody, []byte(code)) {
		t.Fatalf("want %s, got %+v %s", code, response, response.ResponseBody)
	}
}

func TestConcurrencyOnlyExplicitPositiveLimits(t *testing.T) {
	h := newConcurrencyHarness(t)
	// Old defaults and inherited values must never introduce an account limit.
	h.configure(map[string]any{
		"concurrency": settings.ConcurrencySettings{DefaultMaxConcurrency: 1, AccountQueueCapacity: 2, QueueTimeoutSeconds: 1},
		"credential_policies": location.CredentialPolicies{
			"zero":      {MaxConcurrency: 0},
			"inherited": {MaxConcurrency: 7, ConcurrencyMode: "inherit"},
			"custom":    {MaxConcurrency: 2, ConcurrencyMode: "custom"},
		},
	})
	for _, index := range []string{"new", "zero", "inherited"} {
		for _, suffix := range []string{"a", "b", "c", "d"} {
			if got := h.call(index+suffix, index); got.Terminate {
				t.Fatalf("unlimited account %s was restricted: %+v", index, got)
			}
		}
		h.app.admissionsMu.Lock()
		count, queued := h.app.upstreamCounts[index], len(h.app.upstreamQueues[index])
		h.app.admissionsMu.Unlock()
		if count != 4 || queued != 0 {
			t.Fatalf("unlimited account bookkeeping: active %d, queued %d", count, queued)
		}
		for _, suffix := range []string{"a", "b", "c", "d"} {
			h.complete(index + suffix)
		}
	}
	for _, id := range []string{"custom1", "custom2"} {
		if h.call(id, "custom").Terminate {
			t.Fatal("explicit limit not preserved")
		}
	}
	waiting := h.async("custom3", "custom")
	h.waiting("custom", 1)
	h.complete("custom1")
	if receiveAdmission(t, waiting).Terminate {
		t.Fatal("positive account limit did not use queue settings")
	}
	h.complete("custom2")
	h.complete("custom3")
}
func TestConcurrencyQueueFIFOAndCapacity(t *testing.T) {
	h := newConcurrencyHarness(t)
	h.config(2, 5, location.CredentialPolicies{"one": {MaxConcurrency: 1}})
	h.call("running", "one")
	first := h.async("first", "one")
	h.waiting("one", 1)
	second := h.async("second", "one")
	h.waiting("one", 2)
	assertConcurrencyError(t, h.call("overflow", "one"), "concurrency_queue_full")
	if h.call("independent", "two").Terminate {
		t.Fatal("independent credential blocked")
	}
	h.complete("running")
	if receiveAdmission(t, first).Terminate {
		t.Fatal("first waiter rejected")
	}
	h.waiting("one", 1)
	select {
	case <-second:
		t.Fatal("second waiter overtook active first")
	default:
	}
	h.complete("first")
	if receiveAdmission(t, second).Terminate {
		t.Fatal("second waiter rejected")
	}
	h.complete("second")
	h.complete("independent")
	h.waiting("one", 0)
	h.app.admissionsMu.Lock()
	defer h.app.admissionsMu.Unlock()
	if len(h.app.upstreamCounts)+len(h.app.upstreamQueued)+len(h.app.upstreamRequests) != 0 {
		t.Fatal("slots leaked")
	}
}
func TestConcurrencyQueueTimeoutAndCancellation(t *testing.T) {
	h := newConcurrencyHarness(t)
	h.config(1, 1, location.CredentialPolicies{"one": {MaxConcurrency: 1}})
	h.call("running", "one")
	canceled := h.async("cancel", "one")
	h.waiting("one", 1)
	h.complete("cancel")
	if !receiveAdmission(t, canceled).Terminate {
		t.Fatal("canceled request admitted")
	}
	h.waiting("one", 0)
	started := time.Now()
	timed := h.async("timed", "one")
	h.waiting("one", 1)
	time.Sleep(300 * time.Millisecond)
	h.config(1, 30, location.CredentialPolicies{"one": {MaxConcurrency: 1}})
	assertConcurrencyError(t, receiveAdmission(t, timed), "concurrency_queue_timeout")
	if time.Since(started) > 2*time.Second {
		t.Fatal("configuration reload extended queue deadline")
	}
	h.waiting("one", 0)
	h.complete("running")
	if h.call("after", "one").Terminate {
		t.Fatal("timeout leaked a slot")
	}
	h.complete("after")
}
func TestConcurrencyQueueReloadAndDisable(t *testing.T) {
	h := newConcurrencyHarness(t)
	h.config(1, 5, location.CredentialPolicies{"one": {MaxConcurrency: 1}})
	h.call("running", "one")
	pending := h.async("pending", "one")
	h.waiting("one", 1)
	h.config(1, 5, location.CredentialPolicies{"one": {MaxConcurrency: 2}})
	if receiveAdmission(t, pending).Terminate {
		t.Fatal("capacity increase did not wake waiter")
	}
	blocked := h.async("blocked", "one")
	h.waiting("one", 1)
	disabled := bytes.Replace(h.base, []byte("enabled: true"), []byte("enabled: false"), 1)
	if _, err := h.app.HandleMethod(MethodPluginReconfigure, mustMarshal(t, LifecycleRequest{ConfigYAML: disabled})); err != nil {
		t.Fatal(err)
	}
	if receiveAdmission(t, blocked).Terminate {
		t.Fatal("disabled plugin blocked waiter")
	}
	h.waiting("one", 0)
	h.complete("running")
	h.complete("pending")
}
func TestConcurrencyChangingToZeroReleasesAllWaiters(t *testing.T) {
	h := newConcurrencyHarness(t)
	h.config(2, 5, location.CredentialPolicies{"one": {MaxConcurrency: 1}})
	h.call("running", "one")
	first := h.async("first", "one")
	h.waiting("one", 1)
	second := h.async("second", "one")
	h.waiting("one", 2)
	h.config(2, 5, location.CredentialPolicies{"one": {MaxConcurrency: 0}})
	for _, pending := range []<-chan RequestInterceptResponse{first, second} {
		if receiveAdmission(t, pending).Terminate {
			t.Fatal("zero limit did not release a waiter")
		}
	}
	h.waiting("one", 0)
	if h.call("next", "one").Terminate {
		t.Fatal("zero limit still rejected new requests")
	}
	for _, id := range []string{"running", "first", "second", "next"} {
		h.complete(id)
	}
}

func TestUnlimitedConcurrencyDoesNotRequireRequestID(t *testing.T) {
	h := newConcurrencyHarness(t)
	h.config(1, 1, location.CredentialPolicies{"zero": {MaxConcurrency: 0}, "limited": {MaxConcurrency: 1}})
	for _, index := range []string{"zero", "new"} {
		response := h.app.admitUpstream(RequestInterceptRequest{Metadata: map[string]any{MetadataSelectedIndex: index}}, nil)
		if response.Terminate {
			t.Fatal("unlimited account failed concurrency identity checks")
		}
	}
	response := h.app.admitUpstream(RequestInterceptRequest{Metadata: map[string]any{MetadataSelectedIndex: "limited"}}, nil)
	if !response.Terminate || response.StatusCode != 503 {
		t.Fatal("limited account admitted without lifecycle tracking")
	}
}
