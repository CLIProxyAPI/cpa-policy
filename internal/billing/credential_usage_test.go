package billing

import (
	"testing"
	"time"
)

func TestLastQuotaCounterResetIgnoresConcurrentReorderingNoise(t *testing.T) {
	start := time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		values []float64
		scale  float64
		reset  int
	}{
		{name: "out of order", values: []float64{40, 41, 40, 42, 30}, scale: 1, reset: -1},
		{name: "low usage", values: []float64{4, 0}, scale: 1, reset: -1},
		{name: "drop to zero", values: []float64{20, 85, 0, 3}, scale: 1, reset: 2},
		{name: "large drop", values: []float64{90, 40, 45}, scale: 1, reset: 1},
		{name: "latest of several", values: []float64{60, 0, 70, 1, 2}, scale: 1, reset: 3},
		{name: "fraction", values: []float64{0.5, 0.004}, scale: 100, reset: 1},
	} {
		readings := make([]QuotaReading, len(test.values))
		for i, value := range test.values {
			readings[i] = QuotaReading{At: start.Add(time.Duration(i) * time.Minute), Value: value}
		}
		want := time.Time{}
		if test.reset >= 0 {
			want = readings[test.reset].At
		}
		if got := lastQuotaCounterReset(readings, test.scale); !got.Equal(want) {
			t.Errorf("%s: reset = %v, want %v", test.name, got, want)
		}
	}
}
