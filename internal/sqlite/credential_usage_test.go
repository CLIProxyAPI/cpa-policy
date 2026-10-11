package sqlite

import (
	"testing"
	"time"
)

func TestCredentialQuotaReadsHeaderValuesAndValueLists(t *testing.T) {
	d := openTestDB(t)
	for i, headers := range []string{
		`{"X-Codex-Plan-Type":"plus","X-Codex-Secondary-Used-Percent":"40","Other":123}`,
		`{"X-Codex-Primary-Used-Percent":["35"],"Other":{"value":"ignored"}}`,
	} {
		if _, err := d.db.Exec(`INSERT INTO request_events(at,scope,auth_index,provider,response_headers_json)
			VALUES(?, 'dummy', 'dummy-auth', 'codex', ?)`, eventStart.Add(time.Duration(i)*time.Minute).UnixNano(), headers); err != nil {
			t.Fatal(err)
		}
	}
	view, err := d.CredentialUsage([]string{"dummy-auth"}, "dummy", eventStart)
	if err != nil {
		t.Fatal(err)
	}
	quota := view["dummy-auth"].Quota
	if len(quota) != 2 {
		t.Fatalf("quota reports: %+v", quota)
	}
	if quota[0].Headers["x-codex-plan-type"][0] != "plus" ||
		quota[0].Headers["x-codex-secondary-used-percent"][0] != "40" ||
		quota[1].Headers["x-codex-primary-used-percent"][0] != "35" {
		t.Fatalf("quota values: %+v", quota)
	}
}
