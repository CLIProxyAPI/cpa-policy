package plugin

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRetiredCapabilitiesAndEndpointsRemoved(t *testing.T) {
	a := newConfiguredApp(t)
	r := registration()
	if r.Capabilities.Scheduler || r.Capabilities.SchedulerAcrossPriorities {
		t.Fatal("retired scheduler still advertised")
	}
	for _, field := range r.Metadata.ConfigFields {
		if field.Name == "routing" || field.Name == "state_file" || strings.Contains(field.Name, "billing") || strings.Contains(field.Name, "api_key") {
			t.Fatalf("retired config advertised: %s", field.Name)
		}
	}
	reg := managementRegistration()
	if len(reg.Resources) != 1 || reg.Resources[0].Path != resourceBase+"/ui" {
		t.Fatal("API-key resource access still registered")
	}
	for _, suffix := range []string{"/keys", "/keys/bind", "/keys/concurrency", "/plans", "/routes", "/routing", "/prices", "/prices/reference", "/analysis", "/analysis/speed", "/events", "/events/keys", "/auth-files/history", "/ai-providers/usage", "/credentials/sync"} {
		for _, method := range []string{"GET", "POST", "PATCH", "PUT", "DELETE"} {
			for _, base := range []string{managementBase, resourceBase} {
				raw, err := a.HandleMethod(MethodManagementHandle, mustMarshal(t, ManagementRequest{Method: method, Path: base + suffix, Body: []byte(`{}`)}))
				if err != nil {
					t.Fatal(err)
				}
				var response ManagementResponse
				decodeResult(t, raw, &response)
				if response.StatusCode != 404 {
					t.Fatalf("%s %s: %d", method, base+suffix, response.StatusCode)
				}
			}
		}
	}
	raw, err := a.HandleMethod("scheduler.pick", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if json.Unmarshal(raw, &env) != nil || env.OK || env.Error.HTTPStatus != 404 {
		t.Fatal("scheduler still active")
	}
}

func TestUnpricedRequestsPassAndSuccessfulUsageIsNotStored(t *testing.T) {
	a := newConfiguredApp(t)
	a.SetHostCaller(func(string, any) (json.RawMessage, error) {
		t.Fatal("admission must not query prices, subscriptions or routes")
		return nil, nil
	})
	request := RequestInterceptRequest{RequestID: "unpriced", SourceFormat: "openai", Model: "completely-new-model", Body: []byte(`{"model":"completely-new-model"}`), Metadata: map[string]any{MetadataSelectedIndex: "upstream-one"}}
	for _, method := range []string{MethodRequestInterceptBefore, MethodRequestInterceptAfter} {
		raw, err := a.HandleMethod(method, mustMarshal(t, request))
		if err != nil {
			t.Fatal(err)
		}
		var result RequestInterceptResponse
		decodeResult(t, raw, &result)
		if result.Terminate || len(result.Body) > 0 {
			t.Fatalf("unpriced request changed or rejected: %+v", result)
		}
	}
	for _, failed := range []bool{false, true} {
		record := UsageRecord{Failed: failed, APIKey: "dummy-secret", Provider: "codex", Model: "unpriced", Failure: UsageFailure{StatusCode: 429, Body: `{"error":{"message":"dummy-secret","type":"rate_limit_error"}}`}}
		if _, err := a.HandleMethod(MethodUsageHandle, mustMarshal(t, record)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := a.store.ReadPage("errors", "", 0, 50)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("failure-only diagnostics: %+v %v", page, err)
	}
	if bytes.Contains(page.Entries[0], []byte("dummy-secret")) {
		t.Fatal("diagnostics leaked downstream key")
	}
}

func TestDisabledPluginDoesNotEnforcePolicies(t *testing.T) {
	a := newTestApp(t)
	defer a.Shutdown()
	config := append(testConfigYAML(t, false), []byte(locationYAML+"\ncredential_policies:\n  one:\n    max_concurrency: 1\n")...)
	if _, err := a.HandleMethod(MethodPluginRegister, mustMarshal(t, LifecycleRequest{ConfigYAML: config})); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		raw, err := a.HandleMethod(MethodRequestInterceptAfter, mustMarshal(t, RequestInterceptRequest{RequestID: "disabled", SourceFormat: "codex", Body: []byte(`{"tools":[{"type":"web_search"}]}`)}))
		if err != nil {
			t.Fatal(err)
		}
		var result RequestInterceptResponse
		decodeResult(t, raw, &result)
		if result.Terminate || len(result.Body) > 0 {
			t.Fatal("disabled plugin changed request")
		}
	}
}
