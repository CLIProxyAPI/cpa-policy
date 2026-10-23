package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"cpa-codex-controls/internal/messages"
	"cpa-codex-controls/internal/moderation"
	"cpa-codex-controls/internal/settings"
)

func (a *App) moderationSettings(_ ManagementRequest) ManagementResponse {
	cfg := a.store.ModerationConfig()
	return JSONResponse(200, map[string]any{"moderation": cfg, "api_key_configured": cfg.API.KeyEnv != "" && strings.TrimSpace(os.Getenv(cfg.API.KeyEnv)) != ""})
}

func (a *App) validateModeration(req ManagementRequest) ManagementResponse {
	cfg := moderation.DefaultConfig()
	if err := decodeStrict(req.Body, &cfg); err != nil {
		return errorResponse(err)
	}
	cfg, err := cfg.Normalize()
	if err != nil {
		return jsonMessageError(400, "invalid_moderation", messages.FromError(err))
	}
	for _, account := range a.store.CredentialPolicies() {
		if account.Moderation != nil && account.Moderation.Policy != nil {
			if err := moderation.ValidateAPIUse(*account.Moderation.Policy, cfg.API); err != nil {
				return jsonMessageError(400, "invalid_moderation", messages.FromError(err))
			}
		}
	}
	return JSONResponse(200, map[string]any{"moderation": cfg})
}

func (a *App) testModeration(req ManagementRequest) ManagementResponse {
	if len(req.Body) > moderation.MaxBodyBytes {
		return JSONError(413, "test_too_large", "Audit test input is too large")
	}
	var test struct {
		Config *moderation.Config `json:"config"`
		Model  string             `json:"model"`
		Text   string             `json:"text"`
		Format string             `json:"format"`
		Body   json.RawMessage    `json:"body"`
	}
	if err := decodeStrict(req.Body, &test); err != nil {
		return errorResponse(err)
	}
	cfg := a.store.ModerationConfig()
	if test.Config != nil {
		cfg = *test.Config
	}
	cfg, err := cfg.Normalize()
	if err != nil {
		return jsonMessageError(400, "invalid_moderation", messages.FromError(err))
	}
	compiled, _ := cfg.Policy.Compile()
	if test.Format == "" {
		test.Format = "openai"
	}
	if len(test.Body) == 0 {
		if test.Format != "openai" {
			return JSONError(400, "invalid_test", "A protocol request body is required when specifying a format")
		}
		test.Body, _ = json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": test.Text}}})
	}
	result := a.moderationEngine.Check(context.Background(), compiled, cfg.API, test.Model, test.Format, test.Body)
	redactModerationLabels(&result, moderationSecrets(req.Headers, cfg.API)...)
	return JSONResponse(200, map[string]any{"result": result})
}

func moderationSecrets(headers http.Header, api moderation.API) []string {
	secret := headers.Get("Authorization")
	if len(secret) >= 7 && strings.EqualFold(secret[:7], "bearer ") {
		secret = strings.TrimSpace(secret[7:])
	}
	return []string{secret, headers.Get("X-Api-Key"), headers.Get("X-Goog-Api-Key"), strings.TrimSpace(os.Getenv(api.KeyEnv))}
}

func redactModerationLabels(result *moderation.Result, secrets ...string) {
	for _, labels := range [][]string{result.Categories, result.RuleNames} {
		for i, label := range labels {
			labels[i] = settings.ModerationExcerpt(label, secrets...)
		}
	}
}

func (a *App) applyModeration(req RequestInterceptRequest, admission *requestAdmission) RequestInterceptResponse {
	if generate, ok := req.Metadata[MetadataGenerate].(bool); ok && !generate {
		return RequestInterceptResponse{}
	}
	index := metadataString(req.Metadata, MetadataSelectedIndex)
	compiled, api, hasOverrides := a.store.ModerationSnapshot(index)
	ctx := context.Background()
	if admission != nil {
		ctx = admission.ctx
	}
	model := req.RequestedModel
	if model == "" {
		model = req.Model
	}
	policy := moderation.DefaultPolicy()
	if compiled != nil {
		policy = compiled.Policy
	}
	var result moderation.Result
	if index == "" && hasOverrides {
		// No account identity means an account-specific policy cannot be safely selected.
		policy.Mode, policy.FailureAction = "pre_block", "block"
		result = moderation.Failure(policy, "credential_unresolved")
	} else {
		result = a.moderationEngine.Check(ctx, compiled, api, model, req.SourceFormat, req.Body)
	}
	if ctx.Err() != nil {
		return policyErrorResponse(req.SourceFormat, "request_completed", "The request has already completed")
	}
	if result.Status == "flagged" || result.Status == "error" || result.MediaIgnored {
		secrets := moderationSecrets(req.Headers, api)
		redactModerationLabels(&result, secrets...)
		excerpt := ""
		if policy.LogExcerpt {
			excerpt = settings.ModerationExcerpt(result.Text, secrets...)
		}
		event := settings.ModerationEvent{At: a.store.Now(), AuthIndex: index, Model: settings.ModerationExcerpt(model, secrets...), SourceFormat: req.SourceFormat, Result: result, Excerpt: excerpt}
		if err := a.store.AddModeration(event); err != nil {
			a.store.AddPluginLog("error", "Unable to save content audit record")
			if result.Action != "block" {
				result = moderation.Failure(policy, "record_unavailable")
			}
		}
	}
	if result.Action != "block" {
		return RequestInterceptResponse{}
	}
	status, code, message := http.StatusForbidden, "content_policy_violation", "The request was blocked by the configured content policy."
	if result.Status == "error" {
		status, code, message = http.StatusServiceUnavailable, "content_audit_unavailable", "The request could not be audited under the configured content policy."
	}
	body := refusalBody(req.SourceFormat, refusal{anthropicType: "permission_error", openaiType: "content_policy_error", openaiCode: code}, message)
	if result.Status == "error" {
		body = refusalBody(req.SourceFormat, refusal{anthropicType: "api_error", openaiType: "server_error", openaiCode: code}, message)
	}
	if strings.Contains(req.SourceFormat, "gemini") || strings.Contains(req.SourceFormat, "vertex") {
		name := "PERMISSION_DENIED"
		if status == 503 {
			name = "UNAVAILABLE"
		}
		body, _ = json.Marshal(map[string]any{"error": map[string]any{"code": status, "status": name, "message": message}})
	}
	return RequestInterceptResponse{Terminate: true, StatusCode: status, ResponseHeaders: http.Header{"Content-Type": {"application/json; charset=utf-8"}}, ResponseBody: body}
}
