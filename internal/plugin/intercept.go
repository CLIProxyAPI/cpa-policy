package plugin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type requestAdmission struct {
	calls     int
	completed bool
}

func (a *App) beginAdmission(requestID string) *requestAdmission {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil
	}
	a.admissionsMu.Lock()
	defer a.admissionsMu.Unlock()
	admission := a.admissions[requestID]
	if admission == nil {
		admission = &requestAdmission{}
		a.admissions[requestID] = admission
	}
	admission.calls++
	return admission
}
func (a *App) endAdmission(requestID string, admission *requestAdmission) {
	if admission == nil {
		return
	}
	a.admissionsMu.Lock()
	defer a.admissionsMu.Unlock()
	admission.calls--
	if admission.calls == 0 {
		delete(a.admissions, strings.TrimSpace(requestID))
	}
}
func (a *App) interceptBeforeAuth(raw []byte) ([]byte, error) {
	var req RequestInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	return OKEnvelope(RequestInterceptResponse{})
}
func (a *App) interceptAfterAuth(raw []byte) ([]byte, error) {
	var req RequestInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if !a.store.Enabled() {
		return OKEnvelope(RequestInterceptResponse{})
	}
	admission := a.beginAdmission(req.RequestID)
	defer a.endAdmission(req.RequestID, admission)
	response := a.applyRequestLocation(req)
	if !response.Terminate {
		if refusal := a.admitUpstream(req, admission); refusal.Terminate {
			response = refusal
		}
	}
	if response.Terminate {
		a.admissionsMu.Lock()
		a.releaseUpstreamLocked(req.RequestID)
		a.admissionsMu.Unlock()
	}
	return OKEnvelope(response)
}
func (a *App) completeRequest(raw []byte) ([]byte, error) {
	var completion RequestCompletion
	if errUnmarshal := json.Unmarshal(raw, &completion); errUnmarshal != nil {
		return nil, fmt.Errorf("Parse request completion event: %w", errUnmarshal)
	}
	if a != nil && a.store != nil {
		func() {
			a.admissionsMu.Lock()
			defer a.admissionsMu.Unlock()
			if admission := a.admissions[strings.TrimSpace(completion.RequestID)]; admission != nil {
				admission.completed = true
				a.signalAdmissionsLocked()
			}
			a.releaseUpstreamLocked(completion.RequestID)
		}()
	}
	return OKEnvelope(struct{}{})
}
func policyErrorResponse(sourceFormat, code, message string) RequestInterceptResponse {
	return RequestInterceptResponse{Terminate: true, StatusCode: http.StatusServiceUnavailable, ResponseHeaders: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}, ResponseBody: refusalBody(sourceFormat, refusal{anthropicType: "api_error", openaiType: "server_error", openaiCode: code}, message)}
}
func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	raw, exists := metadata[key]
	if !exists || raw == nil {
		return ""
	}
	if value, ok := raw.(string); ok {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

type refusal struct{ anthropicType, openaiType, openaiCode string }

func refusalBody(sourceFormat string, kind refusal, message string) []byte {
	normalized := strings.ToLower(strings.TrimSpace(sourceFormat))
	var payload any
	switch {
	case strings.Contains(normalized, "claude") || strings.Contains(normalized, "anthropic"):
		payload = map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    kind.anthropicType,
				"code":    kind.openaiCode,
				"message": message,
			},
		}
	default: // openai, openai-response, codex, gemini, interactions, and anything new.
		payload = map[string]any{
			"error": map[string]any{
				"message": message,
				"type":    kind.openaiType,
				"code":    kind.openaiCode,
			},
		}
	}
	body, _ := json.Marshal(payload)
	return body
}
