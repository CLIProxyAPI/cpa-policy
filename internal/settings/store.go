package settings

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"cpa-codex-controls/internal/location"
	_ "github.com/mattn/go-sqlite3"
)

const Retention = 30 * 24 * time.Hour

type Store struct {
	mu       sync.RWMutex
	cfg      Config
	compiled *location.Compiled
	db       *sql.DB
}

func NewStore() *Store          { return &Store{cfg: DefaultConfig()} }
func (s *Store) Now() time.Time { return time.Now().UTC() }
func (s *Store) Enabled() bool  { s.mu.RLock(); defer s.mu.RUnlock(); return s.cfg.Enabled }
func (s *Store) LocationOverride() (*location.Compiled, location.Settings) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.compiled, s.cfg.CustomLocation
}
func (s *Store) CredentialPolicies() location.CredentialPolicies {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.cfg.CredentialPolicies)
}
func (s *Store) CredentialPolicySnapshot(index string) (location.CredentialPolicy, *location.Compiled, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.CredentialPolicies[index], s.compiled, len(s.cfg.CredentialPolicies) > 0
}

func (s *Store) Configure(cfg Config) error {
	concurrency, err := cfg.Concurrency.Normalize()
	if err != nil {
		return err
	}
	cfg.Concurrency = concurrency
	compiled, err := cfg.CustomLocation.Compile()
	if err != nil {
		return err
	}
	policies, err := cfg.CredentialPolicies.Normalize()
	if err != nil {
		return err
	}
	cfg.CredentialPolicies = policies
	s.mu.Lock()
	defer s.mu.Unlock()
	db := s.db
	if s.db == nil || cfg.DiagnosticsFile != s.cfg.DiagnosticsFile {
		db, err = openDiagnostics(cfg.DiagnosticsFile)
		if err != nil {
			return err
		}
	}
	if err := importLegacyDiagnostics(db, cfg.LegacyStateFile); err != nil {
		if db != s.db {
			_ = db.Close()
		}
		return err
	}
	if db != s.db {
		if s.db != nil {
			_ = s.db.Close()
		}
		s.db = db
	}
	s.cfg, s.compiled = cfg, compiled
	return nil
}

func openDiagnostics(path string) (*sql.DB, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Do not accept an old billing database as the new diagnostic destination.
	// Create the new file with private permissions, preserving existing modes.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite3", u.String()+"?_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*sql.DB, error) { _ = db.Close(); return nil, err }
	var old int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('request_events','api_keys','keys','plans','plugin_logs','request_errors')").Scan(&old); err != nil {
		return fail(err)
	}
	if old > 0 {
		return fail(fmt.Errorf("diagnostics_file points to a billing database; choose a separate file"))
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS diagnostics (
		id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL,
		kind TEXT NOT NULL, level TEXT NOT NULL DEFAULT '', payload TEXT NOT NULL);
		CREATE INDEX IF NOT EXISTS diagnostics_kind_id ON diagnostics(kind,id);
		CREATE INDEX IF NOT EXISTS diagnostics_at ON diagnostics(at);
		CREATE TABLE IF NOT EXISTS diagnostic_imports (source TEXT PRIMARY KEY);`)
	if err != nil {
		return fail(err)
	}
	return db, nil
}

func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		_ = s.db.Close()
		s.db = nil
	}
}

type ErrorDetail struct {
	StatusCode int    `json:"status_code,omitempty"`
	ErrorType  string `json:"error_type,omitempty"`
	Body       string `json:"body,omitempty"`
}
type ErrorEvent struct {
	ID        int64     `json:"id,string"`
	At        time.Time `json:"at"`
	Provider  string    `json:"provider,omitempty"`
	AuthIndex string    `json:"auth_index,omitempty"`
	Model     string    `json:"model,omitempty"`
	LatencyMS int64     `json:"latency_ms,omitempty"`
	TTFTMS    int64     `json:"ttft_ms,omitempty"`
	ErrorDetail
}
type PluginLog struct {
	ID      int64     `json:"id,string"`
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}
type Page struct {
	Entries      []json.RawMessage `json:"entries"`
	NextBeforeID int64             `json:"next_before_id,string,omitempty"`
}

func (s *Store) append(kind, level string, payload any, at time.Time) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return fmt.Errorf("Diagnostic store is unavailable")
	}
	if level == "debug" && !s.cfg.Debug {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM diagnostics WHERE at < ?", s.Now().Add(-Retention).UnixMilli()); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO diagnostics(at,kind,level,payload) VALUES(?,?,?,?)", at.UnixMilli(), kind, level, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) AddPluginLog(level, format string, args ...any) {
	if s == nil {
		return
	}
	at := s.Now()
	_ = s.append("logs", level, PluginLog{At: at, Level: level, Message: RedactText(fmt.Sprintf(format, args...))}, at)
}
func (s *Store) AddError(event ErrorEvent) error {
	event.Body = RedactText(event.Body)
	return s.append("errors", "", event, event.At)
}
func (s *Store) ReadPage(kind, level string, before int64, limit int) (Page, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	page := Page{Entries: []json.RawMessage{}}
	if s.db == nil {
		return page, fmt.Errorf("Diagnostic store is unavailable")
	}
	query := "SELECT id,payload FROM diagnostics WHERE kind=? AND at>=?"
	args := []any{kind, s.Now().Add(-Retention).UnixMilli()}
	if level != "" {
		query += " AND level=?"
		args = append(args, level)
	}
	if before > 0 {
		query += " AND id<?"
		args = append(args, before)
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	var last int64
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return page, err
		}
		if len(page.Entries) == limit {
			page.NextBeforeID = last
			break
		}
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			return page, err
		}
		item["id"], _ = json.Marshal(fmt.Sprint(id))
		encoded, _ := json.Marshal(item)
		page.Entries = append(page.Entries, encoded)
		last = id
	}
	return page, rows.Err()
}
func (s *Store) ClearPluginLogs() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return 0, fmt.Errorf("Diagnostic store is unavailable")
	}
	result, err := s.db.Exec("DELETE FROM diagnostics WHERE kind='logs'")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
