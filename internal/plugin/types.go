// Package plugin implements the CLIProxyAPI plugin RPC contract.
package plugin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

const (
	ABIVersion    uint32 = 1
	SchemaVersion uint32 = 6
)

// Plugin identity. PluginID doubles as the dynamic library file name, the
// `plugins.configs` key, and the Management/resource route segment.
const (
	PluginID   = "cpa-codex-controls"
	PluginName = "cpa-codex-controls"
	Version    = "1.4.2-timezone.14"

	MenuLabel       = "Account Controls"
	MenuDescription = "Manage credential concurrency, timezone overrides, and diagnostics"
)

// CPA requires a nonempty repository. Release builds replace this placeholder
// with the publishing repository using -ldflags -X. It is not a download source.
var GitHubRepository = "https://github.com/YOUR_ACCOUNT/cpa-codex-controls"

const (
	MethodPluginRegister    = "plugin.register"
	MethodPluginReconfigure = "plugin.reconfigure"

	MethodRequestInterceptBefore = "request.intercept_before"
	MethodRequestInterceptAfter  = "request.intercept_after"
	MethodRequestComplete        = "request.complete"

	MethodUsageHandle = "usage.handle"

	MethodManagementRegister = "management.register"
	MethodManagementHandle   = "management.handle"
	MethodQuotaIdentifier    = "quota.identifier"
	MethodQuotaDescribe      = "quota.describe"
	MethodQuotaFetch         = "quota.fetch"
	MethodQuotaReset         = "quota.reset"
)

const (
	MetadataGenerate      = "generate"
	MetadataSelectedIndex = "selected_auth_index"
)

type Envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *EnvelopeError  `json:"error,omitempty"`
}

type EnvelopeError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type LifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

type Registration struct {
	SchemaVersion uint32       `json:"schema_version"`
	Metadata      Metadata     `json:"metadata"`
	Capabilities  Capabilities `json:"capabilities"`
}

type Metadata struct {
	Name             string        `json:"Name"`
	Version          string        `json:"Version"`
	Author           string        `json:"Author"`
	GitHubRepository string        `json:"GitHubRepository"`
	Logo             string        `json:"Logo"`
	ConfigFields     []ConfigField `json:"ConfigFields"`
}

type ConfigField struct {
	Name        string `json:"Name"`
	Type        string `json:"Type"`
	Default     any    `json:"Default"`
	Description string `json:"Description"`
}

type Capabilities struct {
	RequestNormalizer         bool `json:"request_normalizer"`
	RequestInterceptor        bool `json:"request_interceptor"`
	RequestLifecyclePlugin    bool `json:"request_lifecycle_plugin"`
	UsagePlugin               bool `json:"usage_plugin"`
	ManagementAPI             bool `json:"management_api"`
	Scheduler                 bool `json:"scheduler"`
	SchedulerAcrossPriorities bool `json:"scheduler_across_priorities"`
	QuotaProvider             bool `json:"quota_provider"`
}

type RequestCompletion struct {
	RequestID  string `json:"RequestID"`
	Outcome    string `json:"Outcome"`
	StatusCode int    `json:"StatusCode"`
	Error      string `json:"Error"`
}

type RequestInterceptRequest struct {
	Body           []byte         `json:"Body"`
	ToFormat       string         `json:"ToFormat"`
	HostCallbackID string         `json:"host_callback_id,omitempty"`
	RequestID      string         `json:"RequestID"`
	TraceID        string         `json:"TraceID"`
	Headers        http.Header    `json:"Headers"`
	SourceFormat   string         `json:"SourceFormat"`
	Model          string         `json:"Model"`
	RequestedModel string         `json:"RequestedModel"`
	Metadata       map[string]any `json:"Metadata"`
}

type RequestInterceptResponse struct {
	Body            []byte      `json:"Body,omitempty"`
	Terminate       bool        `json:"Terminate,omitempty"`
	StatusCode      int         `json:"StatusCode,omitempty"`
	ResponseHeaders http.Header `json:"ResponseHeaders,omitempty"`
	ResponseBody    []byte      `json:"ResponseBody,omitempty"`
}

// Only failure diagnostics are consumed. Successful usage and token accounting are ignored.
type UsageRecord struct {
	Provider    string        `json:"Provider"`
	AuthIndex   string        `json:"AuthIndex"`
	Model       string        `json:"Model"`
	APIKey      string        `json:"APIKey"`
	RequestedAt time.Time     `json:"RequestedAt"`
	Latency     time.Duration `json:"Latency"`
	TTFT        time.Duration `json:"TTFT"`
	Failed      bool          `json:"Failed"`
	Failure     UsageFailure  `json:"Failure"`
}
type UsageFailure struct {
	StatusCode int    `json:"StatusCode"`
	Body       string `json:"Body"`
}

type ManagementRegistrationResponse struct {
	Routes    []ManagementRoute `json:"routes,omitempty"`
	Resources []ResourceRoute   `json:"resources,omitempty"`
}

type ManagementRoute struct {
	Method      string `json:"Method"`
	Path        string `json:"Path"`
	Description string `json:"Description,omitempty"`
}

type ResourceRoute struct {
	Path        string `json:"Path"`
	Menu        string `json:"Menu,omitempty"`
	Description string `json:"Description,omitempty"`
}

type ManagementRequest struct {
	Method         string      `json:"Method"`
	Path           string      `json:"Path"`
	Headers        http.Header `json:"Headers"`
	Query          url.Values  `json:"Query"`
	Body           []byte      `json:"Body"`
	HostCallbackID string      `json:"host_callback_id,omitempty"`
}

type ManagementResponse struct {
	StatusCode int         `json:"StatusCode,omitempty"`
	Headers    http.Header `json:"Headers,omitempty"`
	Body       []byte      `json:"Body,omitempty"`
}

type QuotaRequest struct {
	AuthIndex      string `json:"auth_index"`
	AuthID         string `json:"auth_id"`
	Provider       string `json:"provider"`
	HostCallbackID string `json:"host_callback_id"`
}

type QuotaResponse struct {
	Subscription *QuotaSubscription `json:"subscription,omitempty"`
	Summary      []QuotaMetric      `json:"summary,omitempty"`
	Groups       []QuotaGroup       `json:"groups,omitempty"`
}
type QuotaSubscription struct {
	Plan string `json:"plan,omitempty"`
}
type QuotaMetric struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Value    float64 `json:"value"`
	Format   string  `json:"format,omitempty"`
	Currency string  `json:"currency,omitempty"`
}
type QuotaGroup struct {
	DisplayName string        `json:"displayName,omitempty"`
	Buckets     []QuotaBucket `json:"buckets,omitempty"`
}
type QuotaBucket struct {
	Window            string  `json:"window,omitempty"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime,omitempty"`
}

type QuotaDescription struct {
	SupportedProviders []string `json:"supported_providers"`
	DisplayName        string   `json:"display_name"`
	SupportsReset      bool     `json:"supports_reset"`
}
type QuotaResetResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}
