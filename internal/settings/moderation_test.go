package settings

import (
	"bytes"
	"path/filepath"
	"testing"

	"cpa-codex-controls/internal/location"
	"cpa-codex-controls/internal/moderation"
)

func TestModerationRecordBoundRetentionAndRestart(t *testing.T) {
	s := NewStore()
	defer s.Close()
	cfg := DefaultConfig()
	cfg.DiagnosticsFile = filepath.Join(t.TempDir(), "records.db")
	if err := s.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	// Seed over-cap history in one transaction, then exercise the production write.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare("INSERT INTO diagnostics(at,kind,payload) VALUES(?,'moderation','{}')")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10005; i++ {
		if _, err = stmt.Exec(s.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	_ = stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s.AddPluginLog("info", "retained log")
	if err := s.AddError(ErrorEvent{At: s.Now(), Model: "retained error"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddModeration(ModerationEvent{At: s.Now(), Result: moderation.Result{Status: "flagged", Action: "block", Text: "must not be stored"}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM diagnostics WHERE kind='moderation'").Scan(&count); err != nil || count != 10000 {
		t.Fatalf("cap %d: %v", count, err)
	}
	if _, err := s.db.Exec("UPDATE diagnostics SET at=? WHERE kind='moderation' AND id=(SELECT min(id) FROM diagnostics WHERE kind='moderation')", s.Now().Add(-2*Retention).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	first, err := s.ReadPage("moderation", "", 0, 1)
	if err != nil || len(first.Entries) != 1 || first.NextBeforeID == 0 {
		t.Fatal(first, err)
	}
	if bytes.Contains(first.Entries[0], []byte("must not be stored")) {
		t.Fatal("raw input persisted")
	}
	second, err := s.ReadPage("moderation", "", first.NextBeforeID, 1)
	if err != nil || len(second.Entries) != 1 || bytes.Equal(first.Entries[0], second.Entries[0]) {
		t.Fatal("pagination duplicated newest record")
	}
	s.Close()
	if err := s.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClearPluginLogs(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"errors", "moderation"} {
		page, err := s.ReadPage(kind, "", 0, 1)
		if err != nil || len(page.Entries) != 1 {
			t.Fatalf("history lost after restart/log clear: %s %v", kind, err)
		}
	}
	// Any synchronous write prunes expired records; no background cleanup exists.
	s.AddPluginLog("info", "prune")
	if err := s.db.QueryRow("SELECT count(*) FROM diagnostics WHERE kind='moderation'").Scan(&count); err != nil || count != 9999 {
		t.Fatalf("retention %d: %v", count, err)
	}
}

func TestModerationAccountSnapshotAndRollback(t *testing.T) {
	s := NewStore()
	defer s.Close()
	cfg := DefaultConfig()
	cfg.DiagnosticsFile = filepath.Join(t.TempDir(), "records.db")
	p := moderation.DefaultPolicy()
	p.Mode = "pre_block"
	p.Rules = []moderation.Rule{{ID: "one", Kind: "keyword", Pattern: "original"}}
	cfg.CredentialPolicies = location.CredentialPolicies{"one": {Moderation: &moderation.Override{Mode: "custom", Policy: &p}}}
	if err := s.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	copy := s.CredentialPolicies()
	copy["one"].Moderation.Policy.Rules[0].Pattern = "changed"
	other, _ := s.CredentialConcurrencyView()
	other["one"].Moderation.Policy.Mode = "off"
	snapshot, _, _ := s.ModerationSnapshot("one")
	if snapshot.Policy.Mode != "pre_block" || snapshot.Policy.Rules[0].Pattern != "original" {
		t.Fatal("mutable policy snapshot")
	}
	cfg.CredentialPolicies["one"].Moderation.Policy.Strategy = "api"
	if err := s.Configure(cfg); err == nil {
		t.Fatal("account API policy accepted without shared API config")
	}
	snapshot, _, _ = s.ModerationSnapshot("one")
	if snapshot.Policy.Strategy != "keywords" {
		t.Fatal("failed configuration replaced live policy")
	}
}
