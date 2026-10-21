package plugin

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
)

func decodeResult(t *testing.T, raw []byte, v any) {
	t.Helper()
	var envelope Envelope
	if errUnmarshal := json.Unmarshal(raw, &envelope); errUnmarshal != nil {
		t.Fatalf("decode envelope: %v (raw=%s)", errUnmarshal, raw)
	}
	if !envelope.OK {
		t.Fatalf("envelope reports failure: %+v", envelope.Error)
	}
	if v != nil {
		if errUnmarshal := json.Unmarshal(envelope.Result, v); errUnmarshal != nil {
			t.Fatalf("decode result: %v (raw=%s)", errUnmarshal, envelope.Result)
		}
	}
}
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, errMarshal := json.Marshal(v)
	if errMarshal != nil {
		t.Fatalf("marshal: %v", errMarshal)
	}
	return raw
}
func testConfigYAML(t *testing.T, enabled bool) []byte {
	t.Helper()
	return []byte("enabled: " + strconv.FormatBool(enabled) +
		"\ndebug: true" +
		"\ndiagnostics_file: \"" + filepath.Join(t.TempDir(), "state.db") + "\"\n")
}
func newConfiguredApp(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	t.Cleanup(app.Shutdown)
	raw, errHandle := app.HandleMethod(MethodPluginRegister, mustMarshal(t, LifecycleRequest{
		ConfigYAML: testConfigYAML(t, true),
	}))
	if errHandle != nil {
		t.Fatalf("plugin.register error = %v", errHandle)
	}
	decodeResult(t, raw, nil)
	return app
}
func newTestApp(t *testing.T) *App { t.Helper(); return NewApp() }
