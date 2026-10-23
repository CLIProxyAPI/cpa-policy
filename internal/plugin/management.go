package plugin

import (
	"bytes"
	"cpa-codex-controls/internal/messages"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	managementBase      = "/v0/management/plugins/" + PluginID
	resourceBase        = "/v0/resource/plugins/" + PluginID
	resourceUIPath      = "/ui"
	routeAuthFiles      = "/auth-files"
	routeAuthQuota      = "/auth-files/quota"
	routeAuthQuotaReset = "/auth-files/quota/reset"
)

type managementEndpoint struct {
	method, path, description string
	handle                    func(*App, ManagementRequest) ManagementResponse
}

var managementEndpoints = []managementEndpoint{
	{"GET", "/moderation", "Read content audit configuration", (*App).moderationSettings},
	{"POST", "/moderation/validate", "Validate content audit configuration", (*App).validateModeration},
	{"POST", "/moderation/test", "Test content audit without forwarding or saving", (*App).testModeration},
	{"GET", "/moderation/events", "List redacted content audit records", func(a *App, r ManagementRequest) ManagementResponse { return a.diagnosticPage(r, "moderation") }},
	{"GET", "/concurrency", "Read default credential concurrency policy", (*App).concurrencySettings},
	{"POST", "/concurrency/validate", "Validate default credential concurrency policy", (*App).validateConcurrency},
	{"GET", "/credential-policies", "Read credential concurrency and timezone policies", (*App).credentialPolicies},
	{"POST", "/credential-policies/validate", "Validate credential policies", (*App).validateCredentialPolicies},
	{"POST", "/location/detect", "Detect proxy exit location without saving", (*App).detectLocation},
	{"GET", "/location", "Read global timezone settings", (*App).locationSettings},
	{"POST", "/location/validate", "Validate global timezone settings", (*App).validateLocation},
	{"GET", routeAuthFiles, "List auth files", (*App).authFiles},
	{"GET", routeAuthQuota, "Query live provider quota", (*App).authQuota},
	{"POST", routeAuthQuotaReset, "Reset upstream quota", (*App).authQuotaReset},
	{"GET", "/errors", "List upstream failures", func(a *App, r ManagementRequest) ManagementResponse { return a.diagnosticPage(r, "errors") }},
	{"GET", "/plugin-logs", "List plugin logs", func(a *App, r ManagementRequest) ManagementResponse { return a.diagnosticPage(r, "logs") }},
	{"DELETE", "/plugin-logs", "Clear plugin logs", func(a *App, _ ManagementRequest) ManagementResponse {
		count, err := a.store.ClearPluginLogs()
		if err != nil {
			return internalErrorResponse(err)
		}
		return JSONResponse(200, map[string]any{"deleted": count})
	}},
}

func managementRegistration() ManagementRegistrationResponse {
	result := ManagementRegistrationResponse{Resources: []ResourceRoute{{Path: resourceBase + resourceUIPath, Menu: MenuLabel, Description: MenuDescription}}}
	for _, e := range managementEndpoints {
		result.Routes = append(result.Routes, ManagementRoute{Method: e.method, Path: managementBase + e.path, Description: e.description})
	}
	return result
}
func (a *App) handleManagement(raw []byte) ([]byte, error) {
	var req ManagementRequest
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return nil, fmt.Errorf("Parse management request: %w", errUnmarshal)
	}
	path := strings.TrimRight(req.Path, "/")
	if path == "" {
		path = req.Path
	}

	if req.Method == http.MethodGet && path == resourceBase+resourceUIPath {
		return OKEnvelope(ManagementResponse{
			StatusCode: http.StatusOK,
			Headers: http.Header{
				"Content-Type":           []string{"text/html; charset=utf-8"},
				"Cache-Control":          []string{"private, no-store"},
				"Pragma":                 []string{"no-cache"},
				"Referrer-Policy":        []string{"no-referrer"},
				"X-Content-Type-Options": []string{"nosniff"},
				"Content-Security-Policy": []string{
					"default-src 'none'; script-src 'unsafe-inline' https://cdn.jsdelivr.net; style-src 'unsafe-inline' https://cdn.jsdelivr.net; " +
						"font-src https://cdn.jsdelivr.net; connect-src 'self'; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'",
				},
			},
			Body: uiHTML,
		})
	}
	if path != managementBase && !strings.HasPrefix(path, managementBase+"/") {
		return OKEnvelope(JSONError(http.StatusNotFound, "not_found", "Management route not found: "+req.Method+" "+req.Path))
	}
	return OKEnvelope(a.routeManagement(req, strings.TrimPrefix(path, managementBase)))
}
func (a *App) routeManagement(req ManagementRequest, suffix string) ManagementResponse {
	for _, e := range managementEndpoints {
		if req.Method == e.method && suffix == e.path {
			return e.handle(a, req)
		}
	}
	return JSONError(404, "not_found", "Management route not found")
}
func decodeStrict(body []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("Invalid request body: %w", err)
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("The request body must contain exactly one JSON value")
	}
	return nil
}
func errorResponse(err error) ManagementResponse {
	return jsonMessageError(400, "invalid", messages.FromError(err))
}
func internalErrorResponse(err error) ManagementResponse {
	return jsonMessageError(500, "internal_error", messages.FromError(err))
}
func (a *App) diagnosticPage(req ManagementRequest, kind string) ManagementResponse {
	before := int64(0)
	limit := 50
	if v := req.Query.Get("before_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return JSONError(400, "invalid", "Invalid before_id")
		}
		before = n
	}
	if v := req.Query.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return JSONError(400, "invalid", "Invalid limit")
		}
		limit = n
	}
	level := req.Query.Get("level")
	if level != "" && level != "debug" && level != "info" && level != "error" {
		return JSONError(400, "invalid", "Invalid log level")
	}
	page, err := a.store.ReadPage(kind, level, before, limit)
	if err != nil {
		return internalErrorResponse(err)
	}
	return JSONResponse(200, page)
}
