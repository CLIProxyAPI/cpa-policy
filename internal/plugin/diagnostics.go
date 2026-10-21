package plugin

import (
	"cpa-codex-controls/internal/settings"
	"encoding/json"
	"strings"
	"time"
)

func (a *App) handleUsage(raw []byte) ([]byte, error) {
	var record UsageRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	if !a.store.Enabled() || !record.Failed {
		return OKEnvelope(struct{}{})
	}
	detail := usageFailureDetails(record.Failure)
	// Never retain the downstream credential if an upstream error echoes it.
	if record.APIKey != "" {
		detail.Body = strings.ReplaceAll(detail.Body, record.APIKey, "[redacted]")
	}
	if len(detail.Body) > 16384 {
		detail.Body = detail.Body[:16384] + "…"
	}
	at := record.RequestedAt
	if at.IsZero() {
		at = a.store.Now()
	}
	err := a.store.AddError(settings.ErrorEvent{At: at, Provider: record.Provider, AuthIndex: record.AuthIndex, Model: record.Model,
		LatencyMS: int64(record.Latency / time.Millisecond), TTFTMS: int64(record.TTFT / time.Millisecond), ErrorDetail: detail})
	if err != nil {
		return nil, err
	}
	return OKEnvelope(struct{}{})
}
