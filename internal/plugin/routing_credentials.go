package plugin

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"cpa-key-billing/internal/billing"
	"cpa-key-billing/internal/messages"
)

type credentialView struct {
	Ref            string           `json:"ref"`
	Source         string           `json:"source"`
	Provider       string           `json:"provider"`
	DisplayName    string           `json:"display_name"`
	DisplayMessage messages.Message `json:"display_name_message,omitzero"`
	Status         string           `json:"status,omitempty"`
	Disabled       bool             `json:"disabled"`
	Unavailable    bool             `json:"unavailable"`
}

var secretLikeToken = regexp.MustCompile(`(?i)(?:(?:sk|key|token)-[a-z0-9_\-]{4,}|[a-z0-9_\-]{24,})`)
var emailLikeToken = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)

func cleanText(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
}

func safeCredentialName(raw, account, provider, ref string) string {
	name := strings.TrimSpace(filepath.Base(strings.ReplaceAll(raw, "\\", "/")))
	if account = strings.TrimSpace(account); account != "" {
		name = strings.ReplaceAll(name, account, billing.PreviewKey(account))
	}
	name = secretLikeToken.ReplaceAllStringFunc(name, billing.PreviewKey)
	name = emailLikeToken.ReplaceAllStringFunc(name, billing.PreviewKey)
	name = cleanText(name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		short := strings.TrimPrefix(ref, "sha256:")
		if len(short) > 8 {
			short = short[:8]
		}
		name = strings.TrimSpace(provider) + " upstream credential " + short
	}
	if len([]byte(name)) > 160 {
		name = string([]byte(name)[:160])
	}
	return name
}

func credentialDisplayName(file hostAuthFile, source, provider, ref string) string {
	if source == billing.CredentialSourceAuthFiles {
		// Authentication-file identity comes only from CPA's explicit email.
		email := cleanText(file.Email)
		if email == "" {
			return "No email provided"
		}
		return email
	}
	name := file.Label
	if strings.TrimSpace(name) == "" {
		name = file.Name
	}
	return safeCredentialName(name, file.Account, provider, ref)
}

func credentialSourceFromHost(file hostAuthFile) string {
	source := strings.ToLower(strings.TrimSpace(file.Source))
	if file.RuntimeOnly {
		return billing.CredentialSourceAIProviders
	}
	if source == "file" || strings.TrimSpace(file.Path) != "" {
		return billing.CredentialSourceAuthFiles
	}
	if source == "memory" || source == "config" || strings.HasPrefix(source, "config:") {
		return billing.CredentialSourceAIProviders
	}
	return ""
}

func routingAllowsCredential(rawID, source, provider string, decision billing.RoutingDecision) bool {
	ref := ""
	if rawID = strings.TrimSpace(rawID); rawID != "" {
		ref = billing.CredentialFingerprint(rawID)
	}
	return decision.AllowsCredential(ref, source, provider)
}

func routingAllowsAuthFile(file hostAuthFile, decision billing.RoutingDecision) bool {
	source := credentialSourceFromHost(file)
	if source == billing.CredentialSourceAIProviders {
		return false
	}
	if source == "" {
		source = billing.CredentialSourceAuthFiles
	}
	provider := file.Provider
	if strings.TrimSpace(provider) == "" {
		provider = file.Type
	}
	return routingAllowsCredential(file.ID, source, provider, decision)
}

func credentialSourceFromCandidate(candidate SchedulerAuthCandidate) string {
	backend := strings.ToLower(strings.TrimSpace(candidate.Attributes["source_backend"]))
	source := strings.ToLower(strings.TrimSpace(candidate.Attributes["source"]))
	runtimeOnly := strings.EqualFold(strings.TrimSpace(candidate.Attributes["runtime_only"]), "true")
	if backend == "config" || backend == "memory" || runtimeOnly || strings.HasPrefix(source, "config:") || source == "memory" || source == "runtime" || source == "runtime_only" {
		return billing.CredentialSourceAIProviders
	}
	if backend == "file" || backend == "git" || backend == "objectstore" || backend == "postgres" || strings.TrimSpace(candidate.Attributes["path"]) != "" || source == "file" || source == "filesystem" || source == "git" || source == "objectstore" || source == "postgres" {
		return billing.CredentialSourceAuthFiles
	}
	return ""
}

// Display data synchronized by the UI is not evidence of credential identity.
// Config credentials use the immutable identity observed in host candidates;
// physical auth files are checked against the current host runtime entry.
func (a *App) selectedCredentialAllowed(req RequestInterceptRequest, decision billing.RoutingDecision) (bool, error) {
	id := metadataString(req.Metadata, MetadataSelectedAuth)
	if id == "" {
		return false, errors.New("The host did not report the selected upstream credential")
	}
	ref := billing.CredentialFingerprint(id)
	if slices.Contains(decision.DeniedCredentialIDs, ref) ||
		len(decision.CredentialProviders) == 0 && len(decision.CredentialIDs) > 0 && !slices.Contains(decision.CredentialIDs, ref) {
		return false, nil
	}
	if len(decision.DeniedCredentialProviders) == 0 && decision.AllowsCredential(ref, "", "") {
		return true, nil
	}
	a.routingMu.Lock()
	identity := a.credentialIdentities[id]
	a.routingMu.Unlock()
	if identity.Source != billing.CredentialSourceAIProviders {
		index := metadataString(req.Metadata, MetadataSelectedIndex)
		if index == "" || a.hostCaller == nil {
			return false, errors.New("The host did not provide enough information to verify the selected upstream credential")
		}
		raw, err := a.hostCaller(hostAuthGetRuntime, map[string]string{"auth_index": index})
		if err != nil {
			return false, errors.New("The host could not provide runtime information for the selected upstream credential")
		}
		var response struct {
			Auth hostAuthFile `json:"auth"`
		}
		if err := json.Unmarshal(raw, &response); err != nil || response.Auth.ID != id || response.Auth.AuthIndex != index {
			return false, errors.New("The host returned inconsistent identity information for the selected upstream credential")
		}
		identity = billing.CredentialProviderSelector{
			Source:   credentialSourceFromHost(response.Auth),
			Provider: strings.ToLower(strings.TrimSpace(firstNonEmptyString(response.Auth.Provider, response.Auth.Type))),
		}
	}
	if identity.Source == "" || identity.Provider == "" {
		return false, errors.New("The host did not identify the source and provider of the selected upstream credential")
	}
	a.routingMu.Lock()
	a.credentialIdentities[id] = identity
	a.routingMu.Unlock()
	return routingAllowsCredential(id, identity.Source, identity.Provider, decision), nil
}

func (a *App) refreshCredentialInventory() error {
	files, err := a.listHostAuthFiles()
	if err != nil {
		return err
	}
	next := make(map[string]credentialView, len(files))
	raw := make(map[string]billing.CredentialProviderSelector, len(files))
	for _, file := range files {
		id := strings.TrimSpace(file.ID)
		if id == "" {
			continue
		}
		ref := billing.CredentialFingerprint(id)
		provider := strings.ToLower(strings.TrimSpace(file.Provider))
		if provider == "" {
			provider = strings.ToLower(strings.TrimSpace(file.Type))
		}
		source := credentialSourceFromHost(file)
		if source == "" {
			continue
		}
		next[ref] = credentialView{Ref: ref, Source: source, Provider: provider,
			DisplayName: credentialDisplayName(file, source, provider, ref), Status: file.Status,
			Disabled: file.Disabled, Unavailable: file.Unavailable}
		if source == billing.CredentialSourceAuthFiles && cleanText(file.Email) == "" {
			item := next[ref]
			item.DisplayMessage = messages.New("No email provided")
			next[ref] = item
		}
		raw[id] = billing.CredentialProviderSelector{Source: source, Provider: provider}
	}
	a.routingMu.Lock()
	// host.auth.list may omit config-backed credentials.
	for ref, item := range a.credentials {
		if item.Source == billing.CredentialSourceAIProviders {
			if _, ok := next[ref]; !ok {
				next[ref] = item
			}
		}
	}
	for id, identity := range a.credentialIdentities {
		if _, listed := raw[id]; !listed && identity.Source == billing.CredentialSourceAIProviders {
			if _, ok := next[billing.CredentialFingerprint(id)]; ok {
				raw[id] = identity
			}
		}
	}
	a.credentials, a.credentialIdentities = next, raw
	a.routingMu.Unlock()
	return nil
}

func (a *App) observeCandidates(candidates []SchedulerAuthCandidate) {
	a.routingMu.Lock()
	defer a.routingMu.Unlock()
	for _, candidate := range candidates {
		id := strings.TrimSpace(candidate.ID)
		if id == "" {
			continue
		}
		ref := billing.CredentialFingerprint(id)
		source := credentialSourceFromCandidate(candidate)
		if source == "" {
			delete(a.credentialIdentities, id)
			continue
		}
		provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
		name := "Configured credential " + shortCredentialRef(ref)
		detail := messages.New("Configured credential %s", shortCredentialRef(ref))
		if source == billing.CredentialSourceAuthFiles {
			name = "No email provided"
			detail = messages.New("No email provided")
		}
		if existing, ok := a.credentials[ref]; ok && existing.DisplayName != "" {
			name = existing.DisplayName
			detail = existing.DisplayMessage
		}
		a.credentials[ref] = credentialView{Ref: ref, Source: source, Provider: provider, DisplayName: name, DisplayMessage: detail, Status: candidate.Status}
		a.credentialIdentities[id] = billing.CredentialProviderSelector{Source: source, Provider: provider}
	}
}

func (a *App) observeCredentialUsage(authIndex, authType, source, scope string) {
	if !strings.EqualFold(strings.TrimSpace(authType), "apikey") {
		return
	}
	if billing.CallerScope(source) == strings.TrimSpace(scope) {
		return
	}
	name := billing.PreviewKey(source)
	if name == "" {
		return
	}
	a.routingMu.Lock()
	defer a.routingMu.Unlock()
	ref := a.credentialRefsByIndex[strings.TrimSpace(authIndex)]
	credential, ok := a.credentials[ref]
	if !ok || credential.Source != billing.CredentialSourceAIProviders {
		return
	}
	credential.DisplayName = name
	credential.DisplayMessage = messages.Message{}
	a.credentials[ref] = credential
}

func (a *App) syncConfiguredCredentials(req ManagementRequest) ManagementResponse {
	var body struct {
		Credentials []struct {
			Ref         string `json:"ref"`
			Provider    string `json:"provider"`
			DisplayName string `json:"display_name"`
			Disabled    bool   `json:"disabled"`
		} `json:"credentials"`
	}
	if errDecode := decodeStrict(req.Body, &body); errDecode != nil {
		return errorResponse(errDecode)
	}
	if len(body.Credentials) > 4096 {
		return JSONError(http.StatusBadRequest, "invalid", "Too many configured credentials")
	}

	next := make(map[string]billing.ConfigCredential, len(body.Credentials))
	for _, item := range body.Credentials {
		ref := strings.ToLower(strings.TrimSpace(item.Ref))
		provider := strings.ToLower(strings.TrimSpace(item.Provider))
		if !billing.ValidCredentialFingerprint(ref) || provider == "" || len(provider) > 160 || cleanText(provider) != provider {
			return JSONError(http.StatusBadRequest, "invalid", "Invalid configured credential identifier")
		}
		preview := cleanText(item.DisplayName)
		// Accept the old UI sentinel during upgrades; new clients send an empty value.
		if preview == "No API key configured" || preview == "未配置 API Key" {
			preview = ""
		}
		next[ref] = billing.ConfigCredential{
			Provider: provider, KeyPreview: billing.PreviewKey(preview), Disabled: item.Disabled,
		}
	}

	if err := func() error {
		a.routingMu.Lock()
		defer a.routingMu.Unlock()
		previous := a.store.ConfigCredentials()
		if err := a.store.SyncConfigCredentials(next); err != nil {
			return err
		}
		a.replaceSyncedCredentials(previous, next)
		return nil
	}(); err != nil {
		return errorResponse(err)
	}
	return JSONResponse(http.StatusOK, map[string]any{"credentials": a.credentialInventory()})
}

// Caller holds routingMu across persistence and inventory publication.
func (a *App) replaceSyncedCredentials(previous, next map[string]billing.ConfigCredential) {
	for id := range a.credentialIdentities {
		ref := billing.CredentialFingerprint(id)
		if _, synced := previous[ref]; synced {
			if _, retained := next[ref]; !retained {
				delete(a.credentialIdentities, id)
			}
		}
	}
	for ref := range previous {
		delete(a.credentials, ref)
	}
	for ref, item := range next {
		name := item.KeyPreview
		var detail messages.Message
		if name == "" {
			name = "No API key configured"
			detail = messages.New("No API key configured")
		}
		status := "active"
		if item.Disabled {
			status = "disabled"
		}
		a.credentials[ref] = credentialView{
			Ref: ref, Source: billing.CredentialSourceAIProviders, Provider: item.Provider,
			DisplayName: name, DisplayMessage: detail, Status: status, Disabled: item.Disabled,
		}
	}
}

func shortCredentialRef(ref string) string {
	value := strings.TrimPrefix(ref, "sha256:")
	if len(value) > 8 {
		value = value[:8]
	}
	return value
}

func (a *App) credentialInventory() []credentialView {
	a.routingMu.Lock()
	defer a.routingMu.Unlock()
	result := make([]credentialView, 0, len(a.credentials))
	for _, item := range a.credentials {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		if result[i].Provider != result[j].Provider {
			return result[i].Provider < result[j].Provider
		}
		return result[i].DisplayName < result[j].DisplayName
	})
	return result
}

// Table summaries use known labels without discovering host credentials.
func (a *App) credentialLabels(refs []string) map[string]string {
	a.routingMu.Lock()
	defer a.routingMu.Unlock()
	labels := make(map[string]string, len(refs))
	for _, ref := range refs {
		if item, ok := a.credentials[ref]; ok {
			labels[ref] = item.Provider + " · " + item.DisplayName
		}
	}
	return labels
}

func (a *App) listCredentials(_ ManagementRequest) ManagementResponse {
	if err := a.refreshCredentialInventory(); err != nil {
		return jsonMessageError(http.StatusBadGateway, "host_unavailable", messages.FromError(err))
	}
	return JSONResponse(http.StatusOK, map[string]any{"credentials": a.credentialInventory()})
}

func (a *App) credentialByRawID(id string) (credentialView, bool) {
	a.routingMu.Lock()
	defer a.routingMu.Unlock()
	id = strings.TrimSpace(id)
	item, ok := a.credentials[billing.CredentialFingerprint(id)]
	if identity, known := a.credentialIdentities[id]; known {
		item.Source, item.Provider = identity.Source, identity.Provider
	}
	return item, ok
}

func (a *App) missingCredentialRef(refs []string) string {
	a.routingMu.Lock()
	defer a.routingMu.Unlock()
	for _, ref := range refs {
		if _, ok := a.credentials[strings.ToLower(strings.TrimSpace(ref))]; !ok {
			return ref
		}
	}
	return ""
}
