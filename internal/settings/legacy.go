package settings

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Import only diagnostics. The old database remains a read-only archive of all
// retired features, including their records and configuration.
func importLegacyDiagnostics(destination *sql.DB, path string) error {
	if path == "" {
		return nil
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	var imported int
	if err = destination.QueryRow("SELECT count(*) FROM diagnostic_imports WHERE source=?", path).Scan(&imported); err != nil {
		return err
	}
	if imported != 0 {
		return nil
	}
	if _, err = os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	sourceURL := url.URL{Scheme: "file", Path: path}
	source, err := sql.Open("sqlite3", sourceURL.String()+"?mode=ro&_query_only=1&_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer source.Close()
	source.SetMaxOpenConns(1)
	// A read transaction provides a consistent snapshot without changing the source.
	snapshot, err := source.Begin()
	if err != nil {
		return fmt.Errorf("Read legacy diagnostics: %w", err)
	}
	defer snapshot.Rollback()
	tables := map[string]bool{}
	rows, err := snapshot.Query("SELECT name FROM sqlite_master WHERE type='table' AND name IN ('plugin_logs','request_events','request_errors')")
	if err != nil {
		return fmt.Errorf("Read legacy diagnostic schema: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(tables) == 0 || tables["request_events"] != tables["request_errors"] {
		return fmt.Errorf("Legacy diagnostic schema is incompatible; the original database was not changed")
	}
	tx, err := destination.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	insert := func(kind, level string, at time.Time, entry any) error {
		payload, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO diagnostics(at,kind,level,payload) VALUES(?,?,?,?)", at.UnixMilli(), kind, level, string(payload))
		return err
	}
	if tables["plugin_logs"] {
		rows, err := snapshot.Query("SELECT at,level,message FROM plugin_logs ORDER BY id")
		if err != nil {
			return fmt.Errorf("Read legacy plugin logs: %w", err)
		}
		for rows.Next() {
			var nanos int64
			var entry PluginLog
			if err = rows.Scan(&nanos, &entry.Level, &entry.Message); err != nil {
				break
			}
			entry.At = time.Unix(0, nanos).UTC()
			entry.Message = RedactText(entry.Message)
			if err = insert("logs", entry.Level, entry.At, entry); err != nil {
				break
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
	}
	if tables["request_events"] {
		rows, err := snapshot.Query(`SELECT r.at,r.provider,r.auth_index,
   COALESCE(NULLIF(r.upstream_model,''),r.billing_model,''),r.latency_ms,r.ttft_ms,
   COALESCE(e.status_code,0),COALESCE(e.error_type,''),COALESCE(e.body,'')
   FROM request_events r LEFT JOIN request_errors e ON e.request_event_id=r.id
   WHERE r.failed<>0 ORDER BY r.id`)
		if err != nil {
			return fmt.Errorf("Read legacy error events: %w", err)
		}
		for rows.Next() {
			var nanos int64
			var entry ErrorEvent
			if err = rows.Scan(&nanos, &entry.Provider, &entry.AuthIndex, &entry.Model, &entry.LatencyMS, &entry.TTFTMS,
				&entry.StatusCode, &entry.ErrorType, &entry.Body); err != nil {
				break
			}
			entry.At = time.Unix(0, nanos).UTC()
			entry.Body = RedactText(entry.Body)
			if err = insert("errors", "", entry.At, entry); err != nil {
				break
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO diagnostic_imports(source) VALUES(?)", path); err != nil {
		return err
	}
	return tx.Commit()
}
