package plugin

import (
	"cpa-codex-controls/internal/location"
	"cpa-codex-controls/internal/messages"
	"cpa-codex-controls/internal/moderation"
	"cpa-codex-controls/internal/settings"
)

func (a *App) credentialPolicies(_ ManagementRequest) ManagementResponse {
	a.admissionsMu.Lock()
	active := make(map[string]int, len(a.upstreamCounts))
	for k, v := range a.upstreamCounts {
		active[k] = v
	}
	waiting := make(map[string]int, len(a.upstreamQueues))
	for index, queue := range a.upstreamQueues {
		waiting[index] = len(queue)
	}
	a.admissionsMu.Unlock()
	policies, defaults := a.store.CredentialConcurrencyView()
	return JSONResponse(200, map[string]any{"policies": policies, "active": active, "waiting": waiting, "concurrency": defaults})
}
func (a *App) validateCredentialPolicies(req ManagementRequest) ManagementResponse {
	var policies location.CredentialPolicies
	if err := decodeStrict(req.Body, &policies); err != nil {
		return errorResponse(err)
	}
	normalized, err := policies.Normalize()
	if err != nil {
		return jsonMessageError(400, "invalid_credential_policy", messages.FromError(err))
	}
	api := a.store.ModerationConfig().API
	for _, policy := range normalized {
		if policy.Moderation != nil && policy.Moderation.Policy != nil {
			if err := moderation.ValidateAPIUse(*policy.Moderation.Policy, api); err != nil {
				return jsonMessageError(400, "invalid_credential_policy", messages.FromError(err))
			}
		}
	}
	return JSONResponse(200, map[string]any{"policies": normalized})
}

func (a *App) concurrencySettings(_ ManagementRequest) ManagementResponse {
	return JSONResponse(200, map[string]any{"concurrency": a.store.ConcurrencySettings()})
}
func (a *App) validateConcurrency(req ManagementRequest) ManagementResponse {
	cfg := settings.DefaultConcurrencySettings()
	if err := decodeStrict(req.Body, &cfg); err != nil {
		return errorResponse(err)
	}
	cfg, err := cfg.Normalize()
	if err != nil {
		return JSONError(400, "invalid_concurrency_policy", err.Error())
	}
	return JSONResponse(200, map[string]any{"concurrency": cfg})
}
