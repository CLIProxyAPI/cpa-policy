package billing

import (
	"testing"
	"time"
)

func TestQuotaWindowPeriodsFollowResets(t *testing.T) {
	base := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }
	type report struct {
		at    int
		used  float64
		reset int // minutes; 0 when the report has no reset time
	}
	type period struct {
		start, end int // end -1 while the period runs
		reason     QuotaPeriodEnd
	}
	for _, test := range []struct {
		name    string
		reports []report
		now     int
		want    []period
	}{
		{"scheduled", []report{{0, 10, 300}, {100, 40, 300}, {310, 5, 610}, {400, 30, 610}}, 500,
			[]period{{0, 300, QuotaPeriodScheduled}, {310, -1, ""}}},
		{"expired without later reports", []report{{0, 10, 300}}, 301, []period{{0, 300, QuotaPeriodScheduled}}},
		{"reset time jitter", []report{{0, 10, 301}, {60, 20, 300}, {120, 30, 301}}, 200, []period{{0, -1, ""}}},
		{"early reset keeping the reset time", []report{{0, 20, 300}, {60, 85, 300}, {61, 84, 300},
			{120, 0, 300}, {121, 3, 300}}, 200,
			[]period{{0, 120, QuotaPeriodEarlyReset}, {120, -1, ""}}},
		{"request that began before the reset", []report{{0, 70, 300}, {100, 79, 300}, {101, 0, 300},
			{102, 80, 300}, {104, 1, 300}}, 200,
			[]period{{0, 104, QuotaPeriodEarlyReset}, {104, -1, ""}}},
		{"reset card starting a new window", []report{{0, 50, 300}, {100, 90, 300}, {130, 0, 430},
			{131, 91, 300}, {135, 1, 430}}, 200,
			[]period{{0, 131, QuotaPeriodEarlyReset}, {131, -1, ""}}},
		{"low usage reset card", []report{{0, 3, 300}, {100, 0, 400}}, 200,
			[]period{{0, 100, QuotaPeriodEarlyReset}, {100, -1, ""}}},
		{"reset time corrected after it passed", []report{{0, 40, 300}, {310, 45, 320}}, 330,
			[]period{{0, 320, QuotaPeriodScheduled}}},
		{"unused window", []report{{0, 0, 300}, {30, 0, 330}, {60, 0, 360}, {90, 2, 360}}, 120,
			[]period{{60, -1, ""}}},
	} {
		reports := make([]QuotaWindowReport, len(test.reports))
		for i, value := range test.reports {
			reports[i] = QuotaWindowReport{At: at(value.at), UsedPercent: value.used}
			if value.reset != 0 {
				reports[i].ResetAt = at(value.reset)
			}
		}
		got := QuotaWindowPeriods(5*time.Hour, reports, at(test.now))
		if len(got) != len(test.want) {
			t.Errorf("%s: periods = %+v", test.name, got)
			continue
		}
		for i, want := range test.want {
			end := time.Time{}
			if want.end >= 0 {
				end = at(want.end)
			}
			if !got[i].Start.Equal(at(want.start)) || !got[i].End.Equal(end) || got[i].EndReason != want.reason {
				t.Errorf("%s: period %d = %+v, want %+v", test.name, i, got[i], want)
			}
		}
	}
}
