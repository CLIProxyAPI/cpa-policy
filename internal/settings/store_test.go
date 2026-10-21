package settings

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIndependentInstallDoesNotOpenOldPluginDatabases(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("plugins", 0700); err != nil {
		t.Fatal(err)
	}
	// Even unreadable-as-SQLite old files must not prevent a fresh install.
	oldFiles := []string{"plugins/cpa-key-billing-state-v1.db", "plugins/cpa-key-billing-diagnostics.db"}
	marker := []byte("another plugin owns this file")
	for _, path := range oldFiles {
		if err := os.WriteFile(path, marker, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := DecodeConfig([]byte("enabled: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore()
	defer s.Close()
	if err := s.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	s.AddPluginLog("info", "independent installation")
	page, err := s.ReadPage("logs", "", 0, 50)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("new diagnostic store: %+v %v", page, err)
	}
	for _, path := range oldFiles {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, marker) {
			t.Fatalf("old plugin data changed: %s: %v", path, err)
		}
	}
}

func TestRetiredConfigIsIgnoredWithoutTouchingHistory(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "billing.db")
	createLegacyFixture(t, old)
	original, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	raw := "enabled: true\nstate_file: " + old + "\ndiagnostics_file: " + filepath.Join(dir, "diagnostics.db") + "\nrouting: {strategy: retired}\ncodex_fast_mode_billing: true\nmask_api_key_view_emails: true\nallow_api_key_quota_reset: true\nproxy_locations: {rules: retired}\n"
	cfg, err := DecodeConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore()
	defer s.Close()
	if err := s.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	s.AddPluginLog("info", "configuration applied")
	got, err := os.ReadFile(old)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("history modified: %v", err)
	}
	if _, err := DecodeConfig([]byte("unknown_setting: true\n")); err == nil {
		t.Fatal("unknown config accepted")
	}
	if _, err := DecodeConfig([]byte("enabled: true\n---\nenabled: false\n")); err == nil {
		t.Fatal("multiple documents accepted")
	}
}

func TestDiagnosticPaginationRetentionAndRestart(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DiagnosticsFile = filepath.Join(t.TempDir(), "diagnostics.db")
	s := NewStore()
	if err := s.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.AddPluginLog("debug", "hidden debug")
	s.AddPluginLog("info", "first")
	s.AddPluginLog("error", "second")
	if err := s.AddError(ErrorEvent{At: s.Now().Add(-2 * Retention), ErrorDetail: ErrorDetail{Body: "expired"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddError(ErrorEvent{At: s.Now(), Provider: "codex", ErrorDetail: ErrorDetail{StatusCode: 429, Body: "test failure"}}); err != nil {
		t.Fatal(err)
	}
	page, err := s.ReadPage("logs", "", 0, 1)
	if err != nil || len(page.Entries) != 1 || page.NextBeforeID == 0 {
		t.Fatalf("first page: %+v %v", page, err)
	}
	next, err := s.ReadPage("logs", "", page.NextBeforeID, 1)
	if err != nil || len(next.Entries) != 1 || next.NextBeforeID != 0 {
		t.Fatalf("next page: %+v %v", next, err)
	}
	var log PluginLog
	if err := json.Unmarshal(next.Entries[0], &log); err != nil || log.Message != "first" {
		t.Fatalf("log: %+v %v", log, err)
	}
	s.Close()
	if err := s.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	page, err = s.ReadPage("errors", "", 0, 50)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("retention/restart: %+v %v", page, err)
	}
	if count, err := s.ClearPluginLogs(); err != nil || count != 2 {
		t.Fatalf("clear: %d %v", count, err)
	}
	page, err = s.ReadPage("errors", "", 0, 50)
	if err != nil || len(page.Entries) != 1 {
		t.Fatal("clearing logs removed failures")
	}
}

func TestConfigurationRollbackAndBillingPathGuard(t *testing.T) {
	s := NewStore()
	defer s.Close()
	cfg := DefaultConfig()
	cfg.DiagnosticsFile = filepath.Join(t.TempDir(), "diagnostics.db")
	if err := s.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(t.TempDir(), "legacy.db")
	db, err := openDiagnostics(old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE request_events(id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	cfg.DiagnosticsFile = old
	cfg.Enabled = true
	if err := s.Configure(cfg); err == nil {
		t.Fatal("legacy database accepted")
	}
	if s.Enabled() {
		t.Fatal("failed reconfiguration changed live state")
	}
}
