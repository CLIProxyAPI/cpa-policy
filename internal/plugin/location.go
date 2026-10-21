package plugin

import (
	"bytes"
	"cpa-codex-controls/internal/location"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (a *App) locationSettings(_ ManagementRequest) ManagementResponse {
	compiled, settings := a.store.LocationOverride()
	zone := ""
	if compiled != nil {
		zone = compiled.Timezone
	}
	return JSONResponse(http.StatusOK, map[string]any{"custom_location": settings, "effective_timezone": zone})
}
func (a *App) validateLocation(req ManagementRequest) ManagementResponse {
	var settings location.Settings
	if err := decodeStrict(req.Body, &settings); err != nil {
		return errorResponse(err)
	}
	compiled, err := settings.Compile()
	if err != nil {
		return JSONError(http.StatusBadRequest, "invalid_location", err.Error())
	}
	if compiled != nil {
		settings.Location = compiled.Location
	}
	return JSONResponse(http.StatusOK, map[string]any{"custom_location": settings})
}
func (a *App) applyRequestLocation(req RequestInterceptRequest) RequestInterceptResponse {
	index := metadataString(req.Metadata, MetadataSelectedIndex)
	credential, global, configured := a.store.CredentialPolicySnapshot(index)
	if req.SourceFormat != "codex" && req.SourceFormat != "openai-response" {
		return RequestInterceptResponse{}
	}
	if configured && index == "" {
		return policyErrorResponse(req.SourceFormat, "location_credential_unresolved", "The host did not report the selected credential index")
	}
	if credential.LocationMode == "preserve" {
		return RequestInterceptResponse{}
	}
	chosen := global
	if credential.LocationMode == "custom" {
		chosen, _ = location.Compile(credential.Location)
	}

	if chosen == nil {
		return RequestInterceptResponse{}
	}
	body, err := location.Rewrite(req.Body, chosen, a.store.Now())
	if err != nil {
		return policyErrorResponse(req.SourceFormat, "location_rewrite_failed", "Unable to apply location policy")
	}
	if bytes.Equal(body, req.Body) {
		return RequestInterceptResponse{}
	}
	return RequestInterceptResponse{Body: body}
}
func (a *App) authFileProxy(index, callback string) (string, error) {
	if a.hostCaller == nil {
		return "", fmt.Errorf("Host auth callback is unavailable")
	}
	raw, err := a.hostCaller(hostAuthGet, map[string]string{"auth_index": index, "host_callback_id": callback})
	if err != nil {
		return "", fmt.Errorf("Unable to read selected auth file proxy")
	}
	var data struct {
		AuthIndex string `json:"auth_index"`
		JSON      struct {
			ProxyURL string `json:"proxy_url"`
		} `json:"json"`
	}
	if json.Unmarshal(raw, &data) != nil || data.AuthIndex != index {
		return "", fmt.Errorf("Auth file proxy response did not match selected credential")
	}
	return strings.TrimSpace(data.JSON.ProxyURL), nil
}
