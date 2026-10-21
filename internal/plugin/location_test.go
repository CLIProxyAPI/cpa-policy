package plugin

import (
	"bytes"
	"net/http"
	"strings"
	"sync"
	"testing"

	"cpa-codex-controls/internal/location"
)

const locationYAML = "\ncustom_location:\n  enabled: true\n  country: US\n  region: California\n  city: Los Angeles\n  timezone: America/Los_Angeles\n"

func TestLocationLifecycleAndRollback(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(app.Shutdown)
	base := string(testConfigYAML(t, true))
	configure := func(extra string) error {
		_, err := app.HandleMethod(MethodPluginReconfigure, mustMarshal(t, LifecycleRequest{ConfigYAML: []byte(base + extra)}))
		return err
	}
	if err := configure(""); err != nil {
		t.Fatal(err)
	}
	if configured, settings := app.store.LocationOverride(); configured != nil || settings.Enabled {
		t.Fatal("enabled by default")
	}
	if err := configure(locationYAML); err != nil {
		t.Fatal(err)
	}
	if err := configure(strings.ReplaceAll(locationYAML, "America/Los_Angeles", "Invalid/Timezone")); err == nil {
		t.Fatal("invalid configuration accepted")
	}
	if configured, _ := app.store.LocationOverride(); configured == nil || configured.Timezone != "America/Los_Angeles" {
		t.Fatal("failed update lost active configuration")
	}
	if err := configure("\ncustom_location:\n  enabled: false\n"); err != nil {
		t.Fatal(err)
	}
	if configured, _ := app.store.LocationOverride(); configured != nil {
		t.Fatal("disabled override remained active")
	}
}

func TestNormalizeLocationProtocolContract(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(app.Shutdown)
	base := string(testConfigYAML(t, true))
	configure := func(extra string) {
		t.Helper()
		if _, err := app.HandleMethod(MethodPluginReconfigure, mustMarshal(t, LifecycleRequest{ConfigYAML: []byte(base + extra)})); err != nil {
			t.Fatal(err)
		}
	}
	body := []byte(`{"input":[{"role":"user","content":[{"type":"input_text","text":"<environment_context><timezone>UTC</timezone></environment_context>"}]}]}`)
	for _, enabled := range []bool{false, true} {
		if enabled {
			configure(locationYAML)
		} else {
			configure("")
		}
		for _, format := range []string{"codex", "openai-response", "claude", "gemini", ""} {
			for range []bool{false, true} {
				raw, err := app.HandleMethod(MethodRequestInterceptAfter, mustMarshal(t, RequestInterceptRequest{SourceFormat: format, Body: body}))
				if err != nil {
					t.Fatal(err)
				}
				var got struct{ Body []byte }
				decodeResult(t, raw, &got)
				if enabled && (format == "codex" || format == "openai-response") {
					if !bytes.Contains(got.Body, []byte("America/Los_Angeles")) {
						t.Fatalf("override missing: %s", got.Body)
					}
				} else if len(got.Body) != 0 {
					t.Fatal("non-target request modified")
				}
			}
		}
	}
	if registration().Capabilities.RequestNormalizer {
		t.Fatal("global normalizer would overwrite selected proxy")
	}
}

func TestLocationValidationDoesNotMutate(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(app.Shutdown)
	if _, err := app.HandleMethod(MethodPluginRegister, mustMarshal(t, LifecycleRequest{ConfigYAML: append(testConfigYAML(t, true), []byte(locationYAML)...)})); err != nil {
		t.Fatal(err)
	}
	settings := location.Settings{Enabled: true, Location: location.Location{Country: "GB", Region: "England", City: "London", Timezone: "Europe/London"}}
	response := app.validateLocation(ManagementRequest{Body: mustMarshal(t, settings)})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("validate: %s", response.Body)
	}
	compiled, _ := app.store.LocationOverride()
	if compiled.Timezone != "America/Los_Angeles" {
		t.Fatal("validation changed live state")
	}
}

func TestConcurrentLocationReconfiguration(t *testing.T) {
	app := newTestApp(t)
	t.Cleanup(app.Shutdown)
	base := string(testConfigYAML(t, true))
	_, err := app.HandleMethod(MethodPluginRegister, mustMarshal(t, LifecycleRequest{ConfigYAML: []byte(base)}))
	if err != nil {
		t.Fatal(err)
	}
	active := mustMarshal(t, LifecycleRequest{ConfigYAML: []byte(base + locationYAML)})
	inactive := mustMarshal(t, LifecycleRequest{ConfigYAML: []byte(base)})
	request := mustMarshal(t, RequestInterceptRequest{SourceFormat: "codex", Body: []byte(`{"tools":[{"type":"web_search"}]}`)})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 30; n++ {
				if _, err := app.HandleMethod(MethodRequestInterceptAfter, request); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	for n := 0; n < 10; n++ {
		for _, raw := range [][]byte{active, inactive} {
			if _, err := app.HandleMethod(MethodPluginReconfigure, raw); err != nil {
				t.Fatal(err)
			}
		}
	}
	wg.Wait()
}
