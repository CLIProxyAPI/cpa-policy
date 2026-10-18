package sqlite

import (
	"testing"
	"time"
)

func insertQuotaHeaders(t *testing.T, d *DB, headers ...string) {
	t.Helper()
	for i, value := range headers {
		if _, err := d.db.Exec(`INSERT INTO request_events(at,scope,auth_index,provider,response_headers_json)
			VALUES(?, 'dummy', 'dummy-auth', 'codex', ?)`, eventStart.Add(time.Duration(i)*time.Minute).UnixNano(), value); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLatestQuotaHeadersReadValuesAndValueLists(t *testing.T) {
	d := openTestDB(t)
	insertQuotaHeaders(t, d,
		`{"X-Codex-Plan-Type":"plus","X-Codex-Secondary-Used-Percent":"40","Other":123}`,
		`{"X-Codex-Primary-Used-Percent":["35"],"Other":{"value":"ignored"}}`,
	)
	view, err := d.LatestQuotaHeaders([]string{"dummy-auth"}, eventStart)
	if err != nil {
		t.Fatal(err)
	}
	quota := view["dummy-auth"]
	if len(quota) != 2 {
		t.Fatalf("quota reports: %+v", quota)
	}
	if quota[0].Headers["x-codex-plan-type"][0] != "plus" ||
		quota[0].Headers["x-codex-secondary-used-percent"][0] != "40" ||
		quota[1].Headers["x-codex-primary-used-percent"][0] != "35" {
		t.Fatalf("quota values: %+v", quota)
	}
}

func TestQuotaHeaderHistoryKeepsTheEndsOfRepeatedReports(t *testing.T) {
	d := openTestDB(t)
	insertQuotaHeaders(t, d,
		`{"X-Codex-Primary-Used-Percent":["10"]}`,
		`{"X-Codex-Primary-Used-Percent":["10"]}`,
		`{"X-Codex-Primary-Used-Percent":["10"]}`,
		`{"X-Codex-Primary-Used-Percent":"20","Other":123}`,
		`{"X-Codex-Primary-Used-Percent":["20"]}`,
	)
	history, err := d.QuotaHeaderHistory("dummy-auth", eventStart, eventStart.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var minutes []int
	for _, observation := range history {
		minutes = append(minutes, int(observation.At.Sub(eventStart)/time.Minute))
	}
	if len(history) != 4 || minutes[0] != 0 || minutes[1] != 2 || minutes[2] != 3 || minutes[3] != 4 ||
		history[2].Headers.Get("X-Codex-Primary-Used-Percent") != "20" || len(history[2].Headers) != 1 {
		t.Fatalf("history at %v: %+v", minutes, history)
	}
}
