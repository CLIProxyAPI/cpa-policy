package plugin

import (
	"cpa-codex-controls/internal/moderation"
	"cpa-codex-controls/internal/settings"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

type App struct {
	store            *settings.Store
	hostCaller       HostCaller
	admissionsMu     sync.Mutex
	admissions       map[string]*requestAdmission
	upstreamRequests map[string]string
	upstreamCounts   map[string]int
	upstreamQueues   map[string][]*upstreamWaiter
	upstreamQueued   map[string]*upstreamWaiter
	admissionSignal  chan struct{}
	stopped          bool
	moderationEngine moderation.Engine
}

func NewApp() *App {
	return &App{store: settings.NewStore(), admissions: map[string]*requestAdmission{}, upstreamRequests: map[string]string{}, upstreamCounts: map[string]int{}, upstreamQueues: map[string][]*upstreamWaiter{}, upstreamQueued: map[string]*upstreamWaiter{}, admissionSignal: make(chan struct{})}
}
func (a *App) SetHostCaller(caller HostCaller) { a.hostCaller = caller }
func (a *App) HandleMethod(method string, request []byte) (response []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			response = nil
			err = fmt.Errorf("Plugin call %s panicked: %v", method, recovered)
			if a != nil && a.store != nil {
				a.store.AddPluginLog("error", "%v", err)
			}
		}
	}()
	return a.handleMethod(method, request)
}
func (a *App) handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case MethodPluginRegister, MethodPluginReconfigure:
		if errConfigure := a.configure(request); errConfigure != nil {
			a.store.AddPluginLog("error", "Failed to apply plugin configuration: %v", errConfigure)
			return nil, errConfigure
		}
		return OKEnvelope(registration())
	case MethodRequestInterceptBefore:
		return a.interceptBeforeAuth(request)
	case "request.normalize":
		return OKEnvelope(struct{}{})
	case MethodRequestInterceptAfter:
		return a.interceptAfterAuth(request)
	case MethodRequestComplete:
		return a.completeRequest(request)
	case MethodUsageHandle:
		return a.handleUsage(request)
	case MethodQuotaIdentifier:
		return OKEnvelope(map[string]string{"identifier": PluginID})
	case MethodQuotaDescribe:
		return OKEnvelope(QuotaDescription{SupportedProviders: authQuotaProviders, DisplayName: MenuLabel, SupportsReset: true})
	case MethodQuotaFetch, MethodQuotaReset:
		return a.handleQuota(method, request)
	case MethodManagementRegister:
		return OKEnvelope(managementRegistration())
	case MethodManagementHandle:
		return a.handleManagement(request)
	default:
		return ErrorEnvelope("unknown_method", "Unsupported plugin method: "+method, http.StatusNotFound), nil
	}
}
func (a *App) Shutdown() {
	if a == nil || a.store == nil {
		return
	}
	a.store.Close()
	a.admissionsMu.Lock()
	a.stopped = true
	for _, admission := range a.admissions {
		admission.cancel()
	}
	a.signalAdmissionsLocked()
	a.upstreamRequests = make(map[string]string)
	a.upstreamCounts = make(map[string]int)
	a.admissionsMu.Unlock()
}
func (a *App) configure(raw []byte) error {
	var req LifecycleRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return fmt.Errorf("Parse plugin lifecycle request: %w", err)
		}
	}
	cfg, err := settings.DecodeConfig(req.ConfigYAML)
	if err != nil {
		return err
	}
	if err = a.store.Configure(cfg); err != nil {
		return err
	}
	a.admissionsMu.Lock()
	a.signalAdmissionsLocked()
	a.admissionsMu.Unlock()
	a.store.AddPluginLog("info", "Plugin configuration applied")
	return nil
}
func registration() Registration {
	defaults := settings.DefaultConfig()
	return Registration{SchemaVersion: SchemaVersion, Metadata: Metadata{
		Name: PluginName, Version: Version, Author: PluginName, GitHubRepository: GitHubRepository, Logo: pluginLogo,
		ConfigFields: []ConfigField{
			{Name: "moderation", Type: "object", Default: defaults.Moderation, Description: "Opt-in synchronous content audit. Credentials are referenced by environment variable; account policies may override global policy."},
			{Name: "concurrency", Type: "object", Default: defaults.Concurrency, Description: "Bounded waiting queues and request-scoped timeouts for accounts with a positive concurrency limit."},
			{Name: "custom_location", Type: "object", Default: defaults.CustomLocation, Description: "Opt-in global location. Disabled by default; individual account settings take precedence."},
			{Name: "credential_policies", Type: "object", Default: map[string]any{}, Description: "Concurrency and timezone policies keyed by CPA auth_index. Zero concurrency means unlimited."},
			{Name: "debug", Type: "boolean", Default: false, Description: "Record plugin debug diagnostics."},
			{Name: "diagnostics_file", Type: "string", Default: defaults.DiagnosticsFile, Description: "Separate diagnostic database. Existing error events and plugin logs are imported read-only from the former state_file."},
		}}, Capabilities: Capabilities{RequestInterceptor: true, RequestLifecyclePlugin: true, UsagePlugin: true, ManagementAPI: true, QuotaProvider: true}}
}
