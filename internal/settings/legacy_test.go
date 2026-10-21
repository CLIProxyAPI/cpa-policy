package settings

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createLegacyFixture(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE plugin_logs(id INTEGER PRIMARY KEY,at INTEGER,level TEXT,message TEXT);
 CREATE TABLE request_events(id INTEGER PRIMARY KEY,at INTEGER,provider TEXT,auth_index TEXT,upstream_model TEXT,billing_model TEXT,failed INTEGER,latency_ms INTEGER,ttft_ms INTEGER);
 CREATE TABLE request_errors(request_event_id INTEGER PRIMARY KEY,status_code INTEGER,error_type TEXT,body TEXT);
 CREATE TABLE api_keys(id INTEGER PRIMARY KEY,secret TEXT);
 INSERT INTO api_keys VALUES(1,'dummy-retired-key');`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixNano()
	for i, msg := range []string{"old configuration applied", "Authorization: Bearer dummy-log-token"} {
		if _, err = db.Exec("INSERT INTO plugin_logs VALUES(?,?,?,?)", i+1, now, "info", msg); err != nil {
			t.Fatal(err)
		}
	}
	for i, failed := range []int{0, 1, 1} {
		if _, err = db.Exec("INSERT INTO request_events VALUES(?,?,?,?,?,?,?,?,?)", i+1, now, "codex", "historical-auth", "upstream-model", "former-alias", failed, 1234, 60); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO request_errors VALUES(2,429,'rate_limit_error','{"error":"historical failure","api_key":"dummy-error-token"}')`); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyDiagnosticsImportOnceAndSourceUnchanged(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "old.db")
	createLegacyFixture(t, source)
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.LegacyStateFile = source
	cfg.DiagnosticsFile = filepath.Join(dir, "new.db")
	s := NewStore()
	defer s.Close()
	for i := 0; i < 3; i++ {
		if err := s.Configure(cfg); err != nil {
			t.Fatal(err)
		}
		logs, err := s.ReadPage("logs", "", 0, 50)
		if err != nil || len(logs.Entries) != 2 {
			t.Fatalf("logs: %+v %v", logs, err)
		}
		errors, err := s.ReadPage("errors", "", 0, 50)
		if err != nil || len(errors.Entries) != 2 {
			t.Fatalf("errors: %+v %v", errors, err)
		}
		var failure ErrorEvent
		if err = json.Unmarshal(errors.Entries[1], &failure); err != nil {
			t.Fatal(err)
		}
		if failure.StatusCode != 429 || failure.Model != "upstream-model" || failure.LatencyMS != 1234 || failure.TTFTMS != 60 || failure.AuthIndex != "historical-auth" {
			t.Fatalf("lost diagnostic details: %+v", failure)
		}
		all, _ := json.Marshal([]Page{logs, errors})
		if bytes.Contains(all, []byte("dummy-")) {
			t.Fatalf("credential imported: %s", all)
		}
		if i == 1 {
			s.Close()
		}
	}
	if _, err = s.ClearPluginLogs(); err != nil {
		t.Fatal(err)
	}
	if err = s.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	logs, err := s.ReadPage("logs", "", 0, 50)
	if err != nil || len(logs.Entries) != 0 {
		t.Fatal("cleared logs reimported")
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("source database modified")
	}
	var retired int
	if err = s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('api_keys','request_events','request_errors','plugin_logs')").Scan(&retired); err != nil || retired != 0 {
		t.Fatal("retired schema copied")
	}
}

func TestIncompatibleLegacyImportRollsBack(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.DiagnosticsFile = filepath.Join(dir, "diagnostics.db")
	cfg.LegacyStateFile = ""
	s := NewStore()
	defer s.Close()
	if err := s.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	s.AddPluginLog("info", "existing log")
	source := filepath.Join(dir, "legacy.db")
	createLegacyFixture(t, source)
	db, err := sql.Open("sqlite3", source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("ALTER TABLE request_events DROP COLUMN latency_ms")
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(source)
	cfg.LegacyStateFile = source
	cfg.Enabled = true
	if err = s.Configure(cfg); err == nil {
		t.Fatal("incompatible schema accepted")
	}
	if s.Enabled() {
		t.Fatal("failed import changed live config")
	}
	logs, err := s.ReadPage("logs", "", 0, 50)
	if err != nil || len(logs.Entries) != 1 {
		t.Fatal("partial import not rolled back")
	}
	var markers int
	if err = s.db.QueryRow("SELECT count(*) FROM diagnostic_imports").Scan(&markers); err != nil || markers != 0 {
		t.Fatal("failed migration recorded as complete")
	}
	after, _ := os.ReadFile(source)
	if !bytes.Equal(original, after) {
		t.Fatal("incompatible source modified")
	}
}

func TestDiagnosticRedaction(t *testing.T) {
	for _, input := range []string{
		`{"access_token":"dummy-token","nested":{"refresh_token":"dummy-refresh"},"error":"rate limit"}`,
		`Authorization: Bearer dummy-auth; api-key=dummy-key`,
		`upstream rejected sk-dummy-key`,
	} {
		actual := RedactText(input)
		if strings.Contains(actual, "dummy") || !strings.Contains(actual, "[redacted]") {
			t.Fatalf("unredacted: %s", actual)
		}
	}
}
