package plugin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"cpa-key-billing/internal/billing"
	"cpa-key-billing/internal/messages"
)

const (
	hostAuthList = "host.auth.list"
	hostAuthGet  = "host.auth.get"
)

const xaiGrokClientVersion = "1.0.44"

var quotaResetIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type HostCaller func(method string, payload any) (json.RawMessage, error)

type hostAuthFile struct {
	ID          string    `json:"id"`
	AuthIndex   string    `json:"auth_index"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Provider    string    `json:"provider"`
	Label       string    `json:"label"`
	Status      string    `json:"status"`
	Source      string    `json:"source"`
	Path        string    `json:"path"`
	Account     string    `json:"account"`
	Disabled    bool      `json:"disabled"`
	Unavailable bool      `json:"unavailable"`
	Priority    int       `json:"priority"`
	Email       string    `json:"email"`
	ProjectID   string    `json:"project_id"`
	AccountType string    `json:"account_type"`
	RuntimeOnly bool      `json:"runtime_only"`
	ModTime     time.Time `json:"modtime"`
}

type authFileView struct {
	AuthIndex          string                       `json:"auth_index"`
	Name               string                       `json:"name"`
	Category           string                       `json:"category"`
	Email              string                       `json:"email,omitempty"`
	Disabled           bool                         `json:"disabled"`
	Unavailable        bool                         `json:"unavailable"`
	Priority           int                          `json:"priority"`
	QuotaSupported     bool                         `json:"quota_supported"`
	QuotaReason        string                       `json:"quota_unavailable_reason,omitempty"`
	QuotaReasonMessage messages.Message             `json:"quota_unavailable_message,omitzero"`
	CacheRevision      string                       `json:"cache_revision,omitempty"`
	Usage              billing.CredentialUsageStats `json:"usage"`
	Quota              *authQuotaResponse           `json:"quota,omitempty"`
}

type authFileListResponse struct {
	Files []authFileView `json:"files"`
}

type hostAuthListResponse struct {
	Files []hostAuthFile `json:"files"`
}

type quotaRow struct {
	Label            string           `json:"label,omitempty"`
	GroupLabel       string           `json:"group_label,omitempty"`
	LabelMessage     messages.Message `json:"label_message,omitzero"`
	GroupMessage     messages.Message `json:"group_message,omitzero"`
	LabelPrefix      string           `json:"label_prefix,omitempty"`
	RemainingPercent *float64         `json:"remaining_percent,omitempty"`
	Used             *float64         `json:"used,omitempty"`
	Limit            *float64         `json:"limit,omitempty"`
	Currency         string           `json:"currency,omitempty"`
	ResetAt          string           `json:"reset_at,omitempty"`
	ObservedAt       time.Time        `json:"observed_at,omitzero"`
	windowSeconds    int64
}

type authQuotaResponse struct {
	PlanObservedAt                      time.Time           `json:"plan_observed_at,omitzero"`
	CreditBalanceObservedAt             time.Time           `json:"credit_balance_observed_at,omitzero"`
	CreditsUnlimitedObservedAt          time.Time           `json:"credits_unlimited_observed_at,omitzero"`
	AuthRevision                        string              `json:"auth_revision,omitempty"`
	FetchedAt                           time.Time           `json:"fetched_at"`
	Plan                                string              `json:"plan,omitempty"`
	CreditBalance                       string              `json:"credit_balance,omitempty"`
	CreditCurrency                      string              `json:"credit_currency,omitempty"`
	CreditsUnlimited                    bool                `json:"credits_unlimited,omitempty"`
	RateLimitResetCreditsAvailableCount *int                `json:"rate_limit_reset_credits_available_count,omitempty"`
	RateLimitResetCredits               []resetCreditExpiry `json:"rate_limit_reset_credits,omitempty"`
	RateLimitResetCreditsUnavailable    bool                `json:"rate_limit_reset_credits_unavailable,omitempty"`
	Quota                               []quotaRow          `json:"quota"`
}

type resetCreditExpiry struct {
	ExpiresAt string `json:"expires_at"`
}

func (a *App) authFiles(access viewAccess) ManagementResponse {
	if access.APIKey && !access.Tracked {
		return apiKeyUnauthorized()
	}
	files, errList := a.listAuthFiles(access)
	if errList != nil {
		return viewDetailedError(access, http.StatusBadGateway, "host_unavailable", errList)
	}
	return viewJSON(access, http.StatusOK, authFileListResponse{Files: files})
}

func (a *App) authQuota(req ManagementRequest, access viewAccess) ManagementResponse {
	if access.APIKey && !access.Tracked {
		return apiKeyUnauthorized()
	}
	selected, failure := a.resolveQuotaAuthFile(req, access)
	if selected == nil {
		return failure
	}
	result, errQuota := a.fetchAuthQuota(req.HostCallbackID, *selected, authCategory(selected.Type))
	if errQuota != nil {
		return viewDetailedError(access, http.StatusBadGateway, "quota_failed", errQuota)
	}
	return viewJSON(access, http.StatusOK, result)
}

// Resource routes are GET-only. Require a reset ID header and preserve it on retries.
func (a *App) authQuotaReset(req ManagementRequest, access viewAccess) ManagementResponse {
	if access.APIKey && !access.Tracked {
		return apiKeyUnauthorized()
	}
	if access.APIKey && !a.store.AllowAPIKeyQuotaReset() {
		return viewJSONError(access, http.StatusForbidden, "forbidden", "Quota resets are disabled for API key users")
	}
	resetID := req.Headers.Get("X-Quota-Reset-ID")
	if !quotaResetIDPattern.MatchString(resetID) {
		return viewJSONError(access, http.StatusBadRequest, "invalid", "Invalid quota reset request ID")
	}
	selected, failure := a.resolveQuotaAuthFile(req, access)
	if selected == nil {
		return failure
	}
	var reset func(*authQuotaClient, string) error
	switch authCategory(selected.Type) {
	case "codex":
		reset = (*authQuotaClient).resetCodexQuota
	case "claude":
		reset = (*authQuotaClient).resetClaudeQuota
	default:
		return viewJSONError(access, http.StatusUnprocessableEntity, "unsupported", "Quota resets are not supported for this auth file type")
	}
	name := selected.Name
	if access.APIKey && access.MaskEmails {
		name = maskEmails(name)
	}
	if !req.Query.Has("auth_revision") || req.Query.Get("auth_revision") != authFileRevision(*selected) || req.Query.Get("auth_name") != name {
		return viewJSONError(access, http.StatusConflict, "auth_file_changed", "Auth file changed; refresh the auth file list and try again")
	}
	client, errClient := a.openAuthQuotaClient(req.HostCallbackID, *selected)
	if errClient != nil {
		return viewDetailedError(access, http.StatusBadGateway, "reset_failed", errClient)
	}
	defer client.close()
	if errReset := reset(client, resetID); errReset != nil {
		return viewDetailedError(access, http.StatusBadGateway, "reset_failed", errReset)
	}
	// Refresh separately so a failed query cannot obscure a successful reset.
	return viewJSON(access, http.StatusOK, map[string]bool{"reset": true})
}

func (a *App) resolveQuotaAuthFile(req ManagementRequest, access viewAccess) (*hostAuthFile, ManagementResponse) {
	authIndex := strings.TrimSpace(req.Query.Get("auth_index"))
	if authIndex == "" || len(authIndex) > 512 {
		return nil, viewJSONError(access, http.StatusBadRequest, "invalid", "Invalid auth file identifier")
	}
	files, errList := a.listHostAuthFiles()
	if errList != nil {
		return nil, viewDetailedError(access, http.StatusBadGateway, "host_unavailable", errList)
	}
	var selected *hostAuthFile
	for i := range files {
		if files[i].AuthIndex == authIndex {
			selected = &files[i]
			break
		}
	}
	if selected == nil {
		return nil, viewJSONError(access, http.StatusNotFound, "not_found", "Auth file does not exist")
	}
	if strings.EqualFold(strings.TrimSpace(selected.AccountType), "api_key") {
		return nil, viewJSONError(access, http.StatusNotFound, "not_found", "Auth file does not exist")
	}
	if access.APIKey {
		decision := a.store.ResolveRouting(access.Scope, "", "")
		if decision.ConfigurationError != "" || (decision.RestrictsCredentials() && !routingAllowsAuthFile(*selected, decision)) {
			return nil, viewJSONError(access, http.StatusNotFound, "not_found", "Auth file does not exist")
		}
	}
	if selected.Disabled {
		return nil, viewJSONError(access, http.StatusUnprocessableEntity, "disabled", "Auth file is disabled")
	}
	if authCategoryOrder(authCategory(selected.Type)) == 5 {
		return nil, viewJSONError(access, http.StatusUnprocessableEntity, "unsupported", "Quota queries are not supported for this auth file type")
	}
	if selected.RuntimeOnly {
		return nil, viewJSONError(access, http.StatusUnprocessableEntity, "unsupported", "Runtime-only auth files have no readable credentials")
	}
	return selected, ManagementResponse{}
}

func (a *App) listAuthFiles(access viewAccess) ([]authFileView, error) {
	files, errList := a.listHostAuthFiles()
	if errList != nil {
		return nil, errList
	}
	if access.APIKey {
		decision := a.store.ResolveRouting(access.Scope, "", "")
		if decision.ConfigurationError != "" {
			files = nil
		} else if decision.RestrictsCredentials() {
			filtered := files[:0]
			for _, file := range files {
				if routingAllowsAuthFile(file, decision) {
					filtered = append(filtered, file)
				}
			}
			files = filtered
		}
	}
	views := make([]authFileView, 0, len(files))
	for _, file := range files {
		if strings.TrimSpace(file.AuthIndex) == "" || strings.EqualFold(strings.TrimSpace(file.AccountType), "api_key") {
			continue
		}
		category := authCategory(file.Type)
		quotaSupported, quotaReason := authQuotaAvailability(file, category)
		views = append(views, authFileView{
			AuthIndex: file.AuthIndex, Name: file.Name, Category: category, Email: cleanText(file.Email),
			Disabled: file.Disabled, Unavailable: file.Unavailable, Priority: file.Priority,
			QuotaSupported: quotaSupported, QuotaReason: quotaReason, CacheRevision: authFileRevision(file),
			QuotaReasonMessage: messages.Literal(quotaReason),
		})
	}
	indexes := make([]string, len(views))
	for i := range views {
		indexes[i] = views[i].AuthIndex
	}
	scope := ""
	if access.APIKey {
		scope = access.Scope
	}
	usage, errUsage := a.store.CredentialUsage(indexes, scope)
	if errUsage != nil {
		return nil, errUsage
	}
	for i := range views {
		value := usage[views[i].AuthIndex]
		views[i].Usage = value.Stats
		views[i].Quota = observedAuthQuota(views[i].Category, views[i].CacheRevision, value.Quota)
	}
	sort.SliceStable(views, func(i, j int) bool {
		left, right := authCategoryOrder(views[i].Category), authCategoryOrder(views[j].Category)
		if left != right {
			return left < right
		}
		if left == 5 && views[i].Category != views[j].Category {
			return views[i].Category < views[j].Category
		}
		leftName, rightName := strings.ToLower(views[i].Name), strings.ToLower(views[j].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		return views[i].AuthIndex < views[j].AuthIndex
	})
	return views, nil
}

func authQuotaAvailability(file hostAuthFile, category string) (bool, string) {
	if file.Disabled {
		return false, "Auth file is disabled"
	}
	if file.RuntimeOnly {
		return false, "Runtime-only auth files have no readable credentials"
	}
	if authCategoryOrder(category) == 5 {
		return false, "Quota queries are not supported for this auth file type"
	}
	return true, ""
}

func authFileRevision(file hostAuthFile) string {
	if file.ModTime.IsZero() {
		return ""
	}
	return file.ModTime.UTC().Format(time.RFC3339Nano)
}

// Codex plans are labelled like the official Codex client.
var codexPlanLabels = map[string]string{
	"promax": "Pro 500", "pro": "Pro 200", "prolite": "Pro 100", "pro-lite": "Pro 100", "pro_lite": "Pro 100",
	"plus": "Plus", "go": "Go", "free": "Free",
	"self_serve_business_prolite": "Business Premium", "team": "Business", "self_serve_business_usage_based": "Business",
	"business": "Enterprise", "enterprise": "Enterprise", "ent26": "Enterprise", "enterprise_cbp_usage_based": "Enterprise",
	"hc": "Enterprise", "enterprise_cbp_automation": "Enterprise (Automation)",
	"edu_pro": "Edu Pro", "edu_plus": "Edu Plus", "edu": "Edu", "education": "Edu",
}

func codexPlanLabel(plan string) string {
	plan = strings.TrimSpace(plan)
	if label := codexPlanLabels[strings.ToLower(plan)]; label != "" {
		return label
	}
	return plan
}

var creditBalancePattern = regexp.MustCompile(`^\d+(?:\.\d+)?$`)

func codexCreditBalance(value any) string {
	var balance string
	switch value := value.(type) {
	case string:
		balance = strings.TrimSpace(value)
	case float64:
		balance = strconv.FormatFloat(value, 'f', -1, 64)
	}
	if !creditBalancePattern.MatchString(balance) {
		return ""
	}
	return balance
}

func (a *App) listHostAuthFiles() ([]hostAuthFile, error) {
	if a == nil || a.hostCaller == nil {
		return nil, messages.Errorf("This CLIProxyAPI version does not support reading auth files")
	}
	raw, errCall := a.hostCaller(hostAuthList, map[string]any{})
	if errCall != nil {
		return nil, messages.Errorf("Read auth file list: %w", errCall)
	}
	var response hostAuthListResponse
	if errDecode := json.Unmarshal(raw, &response); errDecode != nil {
		return nil, messages.Errorf("Parse auth file list: %w", errDecode)
	}
	return response.Files, nil
}

func authCategory(authType string) string {
	return strings.ToLower(strings.TrimSpace(authType))
}

func authCategoryOrder(category string) int {
	switch category {
	case "claude":
		return 0
	case "antigravity":
		return 1
	case "codex":
		return 2
	case "xai":
		return 3
	case "kimi":
		return 4
	default:
		return 5
	}
}

func (a *App) fetchAuthQuota(callbackID string, file hostAuthFile, provider string) (authQuotaResponse, error) {
	client, errClient := a.openAuthQuotaClient(callbackID, file)
	if errClient != nil {
		return authQuotaResponse{}, errClient
	}
	defer client.close()
	credential := client.credential
	result := authQuotaResponse{AuthRevision: authFileRevision(file), FetchedAt: time.Now().UTC(), Quota: []quotaRow{}}
	if provider == "xai" && paidXAICredential(credential) {
		result.Plan = "Paid"
		return result, nil
	}
	var err error
	switch provider {
	case "codex":
		err = client.fetchCodexQuota(&result)
	case "claude":
		err = client.fetchClaudeQuota(&result)
	case "kimi":
		err = client.fetchKimiQuota(&result)
	case "xai":
		err = client.fetchXAIQuota(&result)
	case "antigravity":
		projectID := firstNonEmptyString(file.ProjectID, credentialString(credential, "project_id", "projectId", "gemini_virtual_project"))
		if projectID == "" {
			return result, messages.Errorf("Auth file is missing project_id")
		}
		err = client.fetchAntigravityQuota(projectID, &result)
	}
	return result, err
}

func (c *authQuotaClient) resetCodexQuota(resetID string) error {
	headers := http.Header{"User-Agent": {"codex_cli_rs/0.76.0"}}
	if accountID := credentialString(c.credential, "account_id", "accountId", "chatgpt_account_id", "chatgptAccountId"); accountID != "" {
		headers.Set("Chatgpt-Account-Id", accountID)
	}
	_, errCall := c.upstreamCall(
		http.MethodPost, "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume",
		headers, map[string]string{"redeem_request_id": resetID},
	)
	return errCall
}

var (
	errClaudeResetNotLimited = messages.Errorf("No reset was used: the Claude account is not rate limited")
	errClaudeResetCooldown   = messages.Errorf("No reset was used: a Claude reset cooldown is active")
	errClaudeResetUnusable   = messages.Errorf("No reset was used: no Claude reset grant is usable")
)

var (
	claudeOrganizationPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	claudeResetGrantIDPattern = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)
)

// The claim re-reads the grants and spends one only when it is usable now.
func (c *authQuotaClient) resetClaudeQuota(resetID string) error {
	headers := http.Header{"Anthropic-Beta": {"oauth-2025-04-20"}}
	profile, errProfile := c.upstream(http.MethodGet, "https://api.anthropic.com/api/oauth/profile", headers, nil)
	if errProfile != nil {
		return errProfile
	}
	organization := strings.ToLower(firstString(objectMap(profile, "organization"), "uuid"))
	if !claudeOrganizationPattern.MatchString(organization) {
		return messages.Errorf("Invalid upstream response")
	}
	status, errStatus := c.claudeResetStatus(headers)
	if errStatus != nil {
		return errStatus
	}
	grantID, errGrant := status.usableGrant(time.Now())
	if errGrant != nil {
		return errGrant
	}
	response, errClaim := c.upstreamCall(
		http.MethodPost, "https://api.anthropic.com/api/organizations/"+organization+"/reset_rate_limits",
		headers, map[string]string{"program": "cedar_ember", "grant_id": grantID, "request_id": resetID},
	)
	if errClaim != nil {
		return errClaim
	}
	switch firstString(response, "result") {
	case "reset", "already_used":
		return nil
	case "not_limited":
		return errClaudeResetNotLimited
	case "cooldown":
		return errClaudeResetCooldown
	case "ineligible", "unavailable":
		return errClaudeResetUnusable
	}
	return messages.Errorf("Claude did not confirm the reset")
}

func (c *authQuotaClient) claudeResetStatus(headers http.Header) (claudeResetStatus, error) {
	object, errCall := c.upstream(http.MethodGet, "https://api.anthropic.com/api/oauth/usage?cedar_ember=1&skip_spend=1", headers, nil)
	if errCall != nil {
		return claudeResetStatus{}, errCall
	}
	status, ok := parseClaudeResetStatus(objectMap(object, "cedar_ember"))
	if !ok {
		return claudeResetStatus{}, messages.Errorf("Invalid upstream response")
	}
	return status, nil
}

// claudeResetStatus is Anthropic's cedar_ember reset-grant block.
type claudeResetStatus struct {
	eligible, atLimit bool
	next              string
	cooldownUntil     time.Time
	grants            []claudeResetGrant
}

type claudeResetGrant struct {
	id                         string
	left                       int
	startsAt, endsAt           time.Time
	paused, usable, needsLimit bool
}

// One malformed grant rejects the whole block, so a partial read never offers a reset.
func parseClaudeResetStatus(block map[string]any) (claudeResetStatus, bool) {
	eligible, okEligible := block["eligible"].(bool)
	rawGrants, okGrants := block["grants"].([]any)
	atLimit, okLimit := strictFlag(block, "at_limit", false)
	cooldown, okCooldown := strictTime(block, "cooldown_until")
	if !okEligible || block["grants"] != nil && !okGrants || !okLimit || !okCooldown {
		return claudeResetStatus{}, false
	}
	status := claudeResetStatus{eligible: eligible, atLimit: atLimit, cooldownUntil: cooldown}
	seen := map[string]bool{}
	for _, raw := range rawGrants {
		item, _ := raw.(map[string]any)
		grant, ok := parseClaudeResetGrant(item)
		if !ok || seen[grant.id] {
			return claudeResetStatus{}, false
		}
		seen[grant.id] = true
		status.grants = append(status.grants, grant)
	}
	if next, _ := block["next_grant_id"].(string); seen[next] {
		status.next = next
	}
	return status, true
}

func parseClaudeResetGrant(item map[string]any) (claudeResetGrant, bool) {
	id, _ := item["id"].(string)
	total, okTotal := item["resets_total"].(float64)
	left, okLeft := item["resets_left"].(float64)
	startsAt, okStart := strictTime(item, "starts_at")
	endsAt, okEnd := strictTime(item, "ends_at")
	paused, okPaused := strictFlag(item, "paused", false)
	// A grant that does not say it is usable is not offered for spending.
	usable, okUsable := strictFlag(item, "usable_now", false)
	needsLimit, okNeeds := strictFlag(item, "use_requires_limit", true)
	ok := claudeResetGrantIDPattern.MatchString(id) && okTotal && okLeft && left >= 0 && left <= total &&
		left == math.Trunc(left) && total == math.Trunc(total) && okStart && okEnd && okPaused && okUsable && okNeeds
	return claudeResetGrant{id: id, left: int(left), startsAt: startsAt, endsAt: endsAt, paused: paused, usable: usable, needsLimit: needsLimit}, ok
}

// usableGrant prefers the upstream recommendation, then the lowest usable ID.
func (s claudeResetStatus) usableGrant(now time.Time) (string, error) {
	if s.cooldownUntil.After(now) {
		return "", errClaudeResetCooldown
	}
	var usable []string
	notLimited := false
	for _, grant := range s.grants {
		if !s.eligible || grant.paused || !grant.usable || grant.left <= 0 || grant.startsAt.After(now) || !grant.endsAt.IsZero() && !grant.endsAt.After(now) {
			continue
		}
		if grant.needsLimit && !s.atLimit {
			notLimited = true
			continue
		}
		if grant.id == s.next {
			return grant.id, nil
		}
		usable = append(usable, grant.id)
	}
	if len(usable) > 0 {
		return slices.Min(usable), nil
	}
	if notLimited {
		return "", errClaudeResetNotLimited
	}
	return "", errClaudeResetUnusable
}

func strictFlag(object map[string]any, key string, fallback bool) (bool, bool) {
	switch value := object[key].(type) {
	case nil:
		return fallback, true
	case bool:
		return value, true
	}
	return false, false
}

func strictTime(object map[string]any, key string) (time.Time, bool) {
	switch value := object[key].(type) {
	case nil:
		return time.Time{}, true
	case string:
		parsed, err := time.Parse(time.RFC3339, value)
		return parsed, err == nil
	}
	return time.Time{}, false
}

func (c *authQuotaClient) fetchCodexQuota(result *authQuotaResponse) error {
	result.Plan = codexPlanLabel(credentialString(c.credential, "plan_type", "planType"))
	headers := http.Header{"User-Agent": {"codex_cli_rs/0.76.0"}, "Content-Type": {"application/json"}}
	if accountID := credentialString(c.credential, "account_id", "accountId", "chatgpt_account_id", "chatgptAccountId"); accountID != "" {
		headers.Set("Chatgpt-Account-Id", accountID)
	}
	object, errCall := c.upstream(http.MethodGet, "https://chatgpt.com/backend-api/wham/usage", headers, nil)
	if errCall != nil {
		return errCall
	}
	if plan := firstString(object, "plan_type", "planType"); plan != "" {
		result.Plan = codexPlanLabel(plan)
	}
	credits := objectMap(object, "credits")
	result.CreditBalance, result.CreditsUnlimited = codexCreditBalance(credits["balance"]), credits["unlimited"] == true
	appendCodexRateLimit(result, "", objectMap(object, "rate_limit", "rateLimit"))
	appendCodexRateLimit(result, "Code Review ", objectMap(object, "code_review_rate_limit", "codeReviewRateLimit"))
	for _, raw := range objectSlice(object, "additional_rate_limits", "additionalRateLimits") {
		additional, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := firstString(additional, "limit_name", "limitName")
		if name == "" {
			name = "Additional"
		}
		appendCodexRateLimit(result, name+" ", objectMap(additional, "rate_limit", "rateLimit"))
	}
	usageCredits := normalizeCodexResetCredits(objectMap(object, "rate_limit_reset_credits", "rateLimitResetCredits"))
	result.RateLimitResetCreditsAvailableCount = usageCredits.availableCount
	headers.Set("Accept", "application/json")
	headers.Set("OpenAI-Beta", "codex-1")
	headers.Set("Originator", "Codex Desktop")
	credits, err := c.upstream(http.MethodGet, "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits", headers, nil)
	details := normalizeCodexResetCredits(credits)
	result.RateLimitResetCreditsUnavailable = err != nil || details.invalidPayload
	if result.RateLimitResetCreditsUnavailable {
		return nil
	}
	result.RateLimitResetCredits = details.credits
	// Match CPAMC: detail count, then non-empty available details, then usage count.
	if details.availableCount != nil {
		result.RateLimitResetCreditsAvailableCount = details.availableCount
	} else if count := len(details.credits); count > 0 {
		result.RateLimitResetCreditsAvailableCount = &count
	}
	return nil
}

type codexResetCreditsSummary struct {
	availableCount *int
	credits        []resetCreditExpiry
	invalidPayload bool
}

func normalizeCodexResetCredits(object map[string]any) codexResetCreditsSummary {
	result := codexResetCreditsSummary{invalidPayload: true}
	for _, key := range []string{"credits", "available_count", "availableCount", "applicable_available_count", "applicableAvailableCount"} {
		if _, ok := object[key]; ok {
			result.invalidPayload = false
			break
		}
	}
	if count, ok := floatValue(object, "available_count", "availableCount"); ok && !math.IsNaN(count) && !math.IsInf(count, 0) {
		value := int(count)
		result.availableCount = &value
	}
	for _, raw := range objectSlice(object, "credits") {
		credit, ok := raw.(map[string]any)
		if !ok || firstString(credit, "reset_type", "resetType") != "codex_rate_limits" || firstString(credit, "status") != "available" {
			continue
		}
		expiresAt := firstString(credit, "expires_at", "expiresAt")
		if expiresAt != "" {
			// Preserve non-empty dates as CPAMC does, even if their format is unknown.
			result.credits = append(result.credits, resetCreditExpiry{ExpiresAt: expiresAt})
		}
	}
	return result
}

type quotaWindow struct {
	Role             string
	Seconds          int64
	RemainingPercent *float64
	ResetAt          string
}

func appendCodexRateLimit(result *authQuotaResponse, labelPrefix string, info map[string]any) {
	allowed, hasAllowed := boolValue(info, "allowed")
	reached, hasReached := boolValue(info, "limit_reached", "limitReached")
	windows := []quotaWindow{}
	for _, role := range []string{"Primary", "Secondary"} {
		key := strings.ToLower(role) + "_window"
		value := objectMap(info, key, camelKey(key))
		if value == nil {
			continue
		}
		seconds, _ := intValue(value, "limit_window_seconds", "limitWindowSeconds")
		window := quotaWindow{Role: role, Seconds: seconds}
		if used, ok := floatValue(value, "used_percent", "usedPercent"); ok {
			window.RemainingPercent = remainingPercent(100 - used)
		} else if hasReached && reached || hasAllowed && !allowed {
			window.RemainingPercent = remainingPercent(0)
		}
		if reset, ok := intValue(value, "reset_at", "resetAt"); ok && reset > 0 {
			window.ResetAt = time.Unix(reset, 0).UTC().Format(time.RFC3339)
		} else if reset, ok := intValue(value, "reset_after_seconds", "resetAfterSeconds"); ok {
			window.ResetAt = resetAtAfter(result.FetchedAt, reset)
		}
		windows = append(windows, window)
	}
	appendQuotaWindows(result, labelPrefix, windows)
}

func appendQuotaWindows(result *authQuotaResponse, labelPrefix string, windows []quotaWindow) {
	sort.SliceStable(windows, func(i, j int) bool {
		return windows[i].order() < windows[j].order()
	})
	for _, window := range windows {
		label := "5-hour limit"
		if window.Role == "Secondary" {
			label = "Weekly limit"
		}
		switch seconds := window.Seconds; {
		case seconds == 5*60*60:
			label = "5-hour limit"
		case seconds == 7*24*60*60:
			label = "Weekly limit"
		case seconds >= 28*24*60*60 && seconds <= 31*24*60*60:
			label = "Monthly limit"
		case seconds > 0:
			label = strconv.FormatInt(seconds/60, 10) + " min"
		}
		result.Quota = append(result.Quota, quotaRow{Label: labelPrefix + label,
			LabelMessage: messages.Literal(label), LabelPrefix: labelPrefix,
			RemainingPercent: window.RemainingPercent, ResetAt: window.ResetAt})
	}
}

func (window quotaWindow) order() int {
	seconds, role := window.Seconds, window.Role
	switch {
	case seconds == 5*60*60:
		return 0
	case seconds == 7*24*60*60 || seconds >= 28*24*60*60 && seconds <= 31*24*60*60:
		return 1
	case role == "Primary":
		return 2
	default:
		return 3
	}
}

func claudeFableLimit(usage map[string]any) map[string]any {
	var fallback map[string]any
	for _, raw := range objectSlice(usage, "limits") {
		limit, ok := raw.(map[string]any)
		if !ok || !strings.EqualFold(firstString(limit, "kind"), "weekly_scoped") {
			continue
		}
		model := objectMap(objectMap(limit, "scope"), "model")
		name := strings.ToLower(firstString(model, "display_name", "displayName"))
		if name != "fable" && name != "fable 5" {
			continue
		}
		if _, okPercent := floatValue(limit, "percent"); !okPercent {
			continue
		}
		if active, okActive := boolValue(limit, "is_active", "isActive"); okActive && active {
			return limit
		}
		if fallback == nil {
			fallback = limit
		}
	}
	return fallback
}

func (c *authQuotaClient) fetchClaudeQuota(result *authQuotaResponse) error {
	headers := http.Header{"Anthropic-Beta": {"oauth-2025-04-20"}, "Content-Type": {"application/json"}}
	usage, errCall := c.upstream(http.MethodGet, "https://api.anthropic.com/api/oauth/usage", headers, nil)
	if errCall != nil {
		return errCall
	}
	fable := claudeFableLimit(usage)
	for _, window := range []struct{ key, label string }{
		{"five_hour", "5-hour limit"}, {"seven_day", "Weekly limit"},
		{"seven_day_oauth_apps", "OAuth Apps weekly limit"}, {"seven_day_opus", "Opus weekly limit"},
		{"seven_day_sonnet", "Sonnet weekly limit"}, {"seven_day_cowork", "Cowork weekly limit"},
		{"iguana_necktie", "Iguana Necktie"},
	} {
		if window.key == "iguana_necktie" && fable != nil {
			continue
		}
		value := objectMap(usage, window.key, camelKey(window.key))
		if value == nil {
			continue
		}
		row := quotaRow{Label: window.label, LabelMessage: messages.Literal(window.label), ResetAt: quotaResetAt(firstString(value, "resets_at", "resetsAt"))}
		if percent, ok := floatValue(value, "utilization"); ok {
			row.RemainingPercent = remainingPercent(100 - percent)
		}
		result.Quota = append(result.Quota, row)
	}
	if fable != nil {
		percent, _ := floatValue(fable, "percent")
		result.Quota = append(result.Quota, quotaRow{Label: "Fable weekly limit", LabelMessage: messages.New("Fable weekly limit"), RemainingPercent: remainingPercent(100 - percent), ResetAt: quotaResetAt(firstString(fable, "resets_at", "resetsAt"))})
	}
	if extra := objectMap(usage, "extra_usage", "extraUsage"); extra != nil {
		enabled, hasEnabled := boolValue(extra, "is_enabled", "isEnabled")
		if !hasEnabled || enabled {
			row := quotaRow{Label: "Extra usage", LabelMessage: messages.New("Extra usage"), Currency: "USD"}
			if value, ok := usdValue(extra, "used_credits", "usedCredits"); ok {
				row.Used = floatPointer(value)
			}
			if value, ok := usdValue(extra, "monthly_limit", "monthlyLimit"); ok {
				row.Limit = floatPointer(value)
			}
			if value, ok := floatValue(extra, "utilization"); ok {
				row.RemainingPercent = remainingPercent(100 - value)
			}
			result.Quota = append(result.Quota, row)
		}
	}
	if status, errStatus := c.claudeResetStatus(headers); errStatus == nil {
		count := 0
		for _, grant := range status.grants {
			count += grant.left
		}
		result.RateLimitResetCreditsAvailableCount = &count
	}
	if profile, errProfile := c.upstream(http.MethodGet, "https://api.anthropic.com/api/oauth/profile", headers, nil); errProfile == nil {
		account, organization := objectMap(profile, "account"), objectMap(profile, "organization")
		max, hasMax := boolValue(account, "has_claude_max", "hasClaudeMax")
		pro, hasPro := boolValue(account, "has_claude_pro", "hasClaudePro")
		plan := ""
		if hasMax && max {
			plan = "Max"
		} else if hasPro && pro {
			plan = "Pro"
		} else if strings.EqualFold(firstString(organization, "organization_type", "organizationType"), "claude_team") && strings.EqualFold(firstString(organization, "subscription_status", "subscriptionStatus"), "active") {
			plan = "Team"
		} else if hasMax && hasPro && !max && !pro {
			plan = "Free"
		}
		if plan != "" {
			result.Plan = plan
		}
	}
	return nil
}

func (c *authQuotaClient) fetchKimiQuota(result *authQuotaResponse) error {
	usage, errCall := c.upstream(http.MethodGet, "https://api.kimi.com/coding/v1/usages", nil, nil)
	if errCall != nil {
		return errCall
	}
	for _, raw := range objectSlice(usage, "limits") {
		limit, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		detail := objectMap(limit, "detail")
		values := limit
		if meaningfulQuotaValues(detail) {
			values = detail
		}
		windowSeconds := kimiWindowSeconds(limit)
		label := firstNonEmptyString(firstString(limit, "name", "title"), firstString(detail, "name", "title"), windowLabel(windowSeconds), "Quota")
		row := quotaRow{Label: label,
			ResetAt: quotaResetAt(firstString(limit, "reset_at", "resetAt", "resetTime"), firstString(detail, "reset_at", "resetAt", "resetTime"))}
		if firstNonEmptyString(firstString(limit, "name", "title"), firstString(detail, "name", "title")) == "" {
			row.LabelMessage = messages.Literal(label)
		}
		fillQuotaValues(&row, values)
		if row.ResetAt == "" {
			if reset, ok := resetSeconds(limit, detail); ok {
				row.ResetAt = resetAtAfter(result.FetchedAt, reset)
			}
		}
		result.Quota = append(result.Quota, row)
	}
	if summary := objectMap(usage, "usage"); meaningfulQuotaValues(summary) {
		row := quotaRow{Label: firstNonEmptyString(firstString(summary, "title"), "Weekly limit"), ResetAt: quotaResetAt(firstString(summary, "reset_at", "resetAt", "resetTime"))}
		if firstString(summary, "title") == "" {
			row.LabelMessage = messages.New("Weekly limit")
		}
		fillQuotaValues(&row, summary)
		if row.ResetAt == "" {
			if reset, ok := resetSeconds(summary); ok {
				row.ResetAt = resetAtAfter(result.FetchedAt, reset)
			}
		}
		result.Quota = append(result.Quota, row)
	}
	return nil
}

func (c *authQuotaClient) fetchXAIQuota(result *authQuotaResponse) error {
	headers := http.Header{
		"X-Xai-Token-Auth":      {"xai-grok-cli"},
		"X-Grok-Client-Version": {xaiGrokClientVersion},
		"User-Agent":            {"grok-pager/" + xaiGrokClientVersion + " grok-shell/" + xaiGrokClientVersion},
		"Accept":                {"*/*"},
	}
	if userID := credentialString(c.credential, "user_id", "userId", "xai_user_id", "xaiUserId", "sub", "subject"); userID != "" {
		headers.Set("X-Userid", userID)
	}
	weekly, weeklyErr := c.upstream(http.MethodGet, "https://cli-chat-proxy.grok.com/v1/billing?format=credits", headers.Clone(), nil)
	monthly, monthlyErr := c.upstream(http.MethodGet, "https://cli-chat-proxy.grok.com/v1/billing", headers.Clone(), nil)
	if weeklyErr != nil && monthlyErr != nil {
		return weeklyErr
	}
	weeklyConfig, monthlyConfig := objectMap(weekly, "config"), objectMap(monthly, "config")
	if weeklyConfig == nil {
		weeklyConfig = objectMap(objectMap(weekly, "body"), "config")
	}
	if monthlyConfig == nil {
		monthlyConfig = objectMap(objectMap(monthly, "body"), "config")
	}
	if percent, ok := floatValue(weeklyConfig, "creditUsagePercent", "credit_usage_percent"); ok {
		row := quotaRow{Label: "Weekly limit", LabelMessage: messages.New("Weekly limit"), RemainingPercent: remainingPercent(100 - percent), ResetAt: xaiResetAt(weeklyConfig)}
		result.Quota = append(result.Quota, row)
	}
	prepaid, hasPrepaid := usdValue(weeklyConfig, "prepaidBalance", "prepaid_balance")
	if !hasPrepaid {
		prepaid, hasPrepaid = usdValue(monthlyConfig, "prepaidBalance", "prepaid_balance")
	}
	if hasPrepaid && prepaid > 0 {
		result.CreditBalance, result.CreditCurrency = strconv.FormatFloat(prepaid, 'f', -1, 64), "USD"
	}
	monthlyLimit, hasLimit := usdValue(monthlyConfig, "monthlyLimit", "monthly_limit")
	totalUsed, hasUsed := usdValue(monthlyConfig, "used")
	onDemandCap, hasOnDemandCap := usdValue(monthlyConfig, "onDemandCap", "on_demand_cap")
	onDemandUsed, hasOnDemandUsed := usdValue(monthlyConfig, "onDemandUsed", "on_demand_used")
	if !hasOnDemandUsed && hasUsed && hasLimit {
		onDemandUsed = math.Max(0, totalUsed-monthlyLimit)
		hasOnDemandUsed = true
	}
	if hasLimit || hasUsed {
		row := quotaRow{Label: "Monthly allowance", LabelMessage: messages.New("Monthly allowance"), Currency: "USD", ResetAt: quotaResetAt(firstString(monthlyConfig, "billingPeriodEnd", "billing_period_end"))}
		if hasLimit {
			row.Limit = floatPointer(monthlyLimit)
		}
		if hasUsed {
			included := totalUsed
			if hasLimit {
				included = math.Min(totalUsed, math.Max(0, monthlyLimit))
			}
			row.Used = floatPointer(included)
			if hasLimit {
				if monthlyLimit > 0 {
					row.RemainingPercent = remainingPercent((1 - included/monthlyLimit) * 100)
				}
			}
		}
		result.Quota = append(result.Quota, row)
	}
	if hasOnDemandCap && onDemandCap > 0 {
		row := quotaRow{Label: "Pay-as-you-go allowance", LabelMessage: messages.New("Pay-as-you-go allowance"), Currency: "USD", Limit: floatPointer(onDemandCap), ResetAt: quotaResetAt(firstString(monthlyConfig, "billingPeriodEnd", "billing_period_end"))}
		if hasOnDemandUsed {
			row.Used = floatPointer(onDemandUsed)
			row.RemainingPercent = remainingPercent((1 - onDemandUsed/onDemandCap) * 100)
		}
		result.Quota = append(result.Quota, row)
	}
	type productQuota struct {
		name, normalized string
		percent          float64
	}
	products := make(map[string]productQuota)
	for _, raw := range objectSlice(weeklyConfig, "productUsage", "product_usage") {
		product, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := strings.Join(strings.Fields(firstString(product, "product")), " ")
		percent, ok := floatValue(product, "usagePercent", "usage_percent")
		if name == "" || !ok {
			continue
		}
		normalized := strings.ToLower(name)
		if current, exists := products[normalized]; !exists || percent > current.percent {
			products[normalized] = productQuota{name: name, normalized: normalized, percent: percent}
		}
	}
	ordered := make([]productQuota, 0, len(products))
	for _, product := range products {
		ordered = append(ordered, product)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].normalized < ordered[j].normalized })
	for _, product := range ordered {
		name, percent := product.name, product.percent
		result.Quota = append(result.Quota, quotaRow{Label: name + " usage", LabelMessage: messages.New("%s usage", name), RemainingPercent: remainingPercent(100 - percent), ResetAt: xaiResetAt(weeklyConfig)})
	}
	return nil
}

func (c *authQuotaClient) fetchAntigravityQuota(projectID string, result *authQuotaResponse) error {
	headers := http.Header{"User-Agent": {"antigravity/cli/1.0.13"}}
	var quota map[string]any
	var lastErr error
	for _, endpoint := range []string{"https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary", "https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary", "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"} {
		value, err := c.upstream(http.MethodPost, endpoint, headers.Clone(), map[string]string{"project": projectID})
		if err != nil {
			lastErr = err
			continue
		}
		if len(objectSlice(value, "groups")) == 0 {
			if body := objectMap(value, "body"); body != nil {
				value = body
			}
		}
		quota = value
		if len(objectSlice(value, "groups")) > 0 {
			break
		}
	}
	if quota == nil {
		return lastErr
	}
	for groupIndex, raw := range objectSlice(quota, "groups") {
		group, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		groupLabel := firstNonEmptyString(firstString(group, "displayName", "display_name"), fmt.Sprintf("Quota group %d", groupIndex+1))
		var groupMessage messages.Message
		if firstString(group, "displayName", "display_name") == "" {
			groupMessage = messages.New("Quota group %d", groupIndex+1)
		}
		groupRows := make([]quotaRow, 0, len(objectSlice(group, "buckets")))
		for _, bucketRaw := range objectSlice(group, "buckets") {
			bucket, ok := bucketRaw.(map[string]any)
			if !ok {
				continue
			}
			remaining, ok := floatValue(bucket, "remainingFraction", "remaining_fraction")
			if !ok {
				continue
			}
			windowName := strings.ToLower(firstString(bucket, "window"))
			label := firstNonEmptyString(firstString(bucket, "displayName", "display_name"), "Quota")
			var windowSeconds int64
			if windowName == "5h" || windowName == "five-hour" || windowName == "five_hour" {
				label = "5-hour limit"
				windowSeconds = 18000
			} else if windowName == "weekly" || windowName == "week" {
				label = "Weekly limit"
				windowSeconds = 604800
			}
			var labelMessage messages.Message
			if windowSeconds > 0 || firstString(bucket, "displayName", "display_name") == "" {
				labelMessage = messages.Literal(label)
			}
			groupRows = append(groupRows, quotaRow{Label: label, GroupLabel: groupLabel, LabelMessage: labelMessage, GroupMessage: groupMessage, RemainingPercent: remainingPercent(remaining * 100), windowSeconds: windowSeconds, ResetAt: quotaResetAt(firstString(bucket, "resetTime", "reset_time"))})
		}
		sort.SliceStable(groupRows, func(i, j int) bool {
			return quotaWindowOrder(groupRows[i].windowSeconds) < quotaWindowOrder(groupRows[j].windowSeconds)
		})
		result.Quota = append(result.Quota, groupRows...)
	}
	for _, endpoint := range []string{"https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist", "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"} {
		subscription, err := c.upstream(http.MethodPost, endpoint, headers.Clone(), map[string]any{"metadata": map[string]string{"ideType": "ANTIGRAVITY"}})
		if err != nil {
			continue
		}
		if body := objectMap(subscription, "body"); body != nil {
			subscription = body
		}
		tier := objectMap(subscription, "paidTier", "paid_tier")
		if tier == nil {
			tier = objectMap(subscription, "currentTier", "current_tier")
		}
		if tier != nil {
			result.Plan = firstNonEmptyString(firstString(tier, "name"), firstString(tier, "description"))
			break
		}
	}
	return nil
}

func quotaWindowOrder(seconds int64) int {
	switch seconds {
	case 18000:
		return 0
	case 604800:
		return 1
	default:
		return 2
	}
}

func objectMap(object map[string]any, keys ...string) map[string]any {
	if object == nil {
		return nil
	}
	for _, key := range keys {
		if value, ok := object[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}
func objectSlice(object map[string]any, keys ...string) []any {
	if object == nil {
		return nil
	}
	for _, key := range keys {
		if value, ok := object[key].([]any); ok {
			return value
		}
	}
	return nil
}

func credentialRecords(credential map[string]any) []map[string]any {
	var records []map[string]any
	var visit func(map[string]any, int)
	visit = func(record map[string]any, depth int) {
		if record == nil || depth > 2 {
			return
		}
		records = append(records, record)
		for _, key := range []string{"metadata", "attributes", "oauth", "raw", "credential", "auth", "token", "installed", "web", "user"} {
			if nested, ok := record[key].(map[string]any); ok {
				visit(nested, depth+1)
			}
		}
	}
	visit(credential, 0)
	return records
}

func credentialString(credential map[string]any, keys ...string) string {
	for _, record := range credentialRecords(credential) {
		if value := firstString(record, keys...); value != "" {
			return value
		}
	}
	return ""
}

func credentialToken(credential map[string]any) string {
	return credentialString(credential, "access_token", "accessToken", "token", "id_token", "idToken", "cookie")
}

func paidXAICredential(credential map[string]any) bool {
	parts := strings.Split(credentialToken(credential), ".")
	if len(parts) < 2 {
		return false
	}
	payload, errDecode := base64.RawURLEncoding.DecodeString(parts[1])
	if errDecode != nil {
		return false
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return false
	}
	for key := range claims {
		normalized := strings.ToLower(key)
		if normalized == "tier" || strings.HasSuffix(normalized, "/tier") || strings.HasSuffix(normalized, ":tier") {
			if tier, ok := floatValue(claims, key); ok && tier >= 1 {
				return true
			}
		}
	}
	return false
}

func credentialUsesAPIKey(credential map[string]any) bool {
	if credentialString(credential, "api_key", "apiKey") != "" {
		return true
	}
	for _, record := range credentialRecords(credential) {
		if usingAPI, ok := boolValue(record, "using_api", "usingApi"); ok && usingAPI {
			return true
		}
	}
	return false
}

func redactSecret(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}

func firstString(object map[string]any, keys ...string) string {
	if object == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func floatValue(object map[string]any, keys ...string) (float64, bool) {
	if object == nil {
		return 0, false
	}
	for _, key := range keys {
		switch value := object[key].(type) {
		case float64:
			return value, true
		case json.Number:
			parsed, err := value.Float64()
			return parsed, err == nil
		case string:
			parsed, err := strconv.ParseFloat(value, 64)
			return parsed, err == nil
		}
	}
	return 0, false
}
func intValue(object map[string]any, keys ...string) (int64, bool) {
	value, ok := floatValue(object, keys...)
	return int64(value), ok
}
func boolValue(object map[string]any, keys ...string) (bool, bool) {
	if object == nil {
		return false, false
	}
	for _, key := range keys {
		switch value := object[key].(type) {
		case bool:
			return value, true
		case float64:
			return value != 0, true
		case string:
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "true", "1", "yes", "y", "on":
				return true, true
			case "false", "0", "no", "n", "off":
				return false, true
			}
		}
	}
	return false, false
}
func floatPointer(value float64) *float64 { return &value }
func remainingPercent(value float64) *float64 {
	return floatPointer(math.Max(0, math.Min(100, value)))
}
func resetAtAfter(fetchedAt time.Time, seconds int64) string {
	if fetchedAt.IsZero() || seconds < 0 {
		return ""
	}
	return fetchedAt.Add(time.Duration(seconds) * time.Second).UTC().Format(time.RFC3339)
}
func quotaResetAt(values ...string) string {
	for _, value := range values {
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
		if err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return ""
}
func camelKey(value string) string {
	parts := strings.Split(value, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}
func meaningfulQuotaValues(object map[string]any) bool {
	if object == nil {
		return false
	}
	for _, key := range []string{"used", "limit", "remaining"} {
		if _, ok := floatValue(object, key); ok {
			return true
		}
	}
	return false
}
func fillQuotaValues(row *quotaRow, object map[string]any) {
	used, hasUsed := floatValue(object, "used")
	limit, hasLimit := floatValue(object, "limit")
	remaining, hasRemaining := floatValue(object, "remaining")
	if !hasUsed && hasLimit {
		used = 0
		if hasRemaining {
			used = limit - remaining
		}
		hasUsed = true
	}
	if hasUsed {
		row.Used = floatPointer(used)
	}
	if hasLimit {
		row.Limit = floatPointer(limit)
	}
	if hasLimit && limit > 0 && hasUsed {
		row.RemainingPercent = remainingPercent((1 - used/limit) * 100)
	}
}
func kimiWindowSeconds(limit map[string]any) int64 {
	window, detail := objectMap(limit, "window"), objectMap(limit, "detail")
	duration, hasDuration := intValue(window, "duration")
	if !hasDuration {
		duration, hasDuration = intValue(limit, "duration")
	}
	if !hasDuration {
		duration, hasDuration = intValue(detail, "duration")
	}
	if !hasDuration {
		return 0
	}
	unit := firstNonEmptyString(firstString(window, "timeUnit", "time_unit"), firstString(limit, "timeUnit", "time_unit"), firstString(detail, "timeUnit", "time_unit"))
	normalized := strings.TrimPrefix(strings.ToLower(unit), "time_unit_")
	multipliers := map[string]int64{"s": 1, "second": 1, "seconds": 1, "m": 60, "minute": 60, "minutes": 60, "h": 3600, "hour": 3600, "hours": 3600, "d": 86400, "day": 86400, "days": 86400, "w": 604800, "week": 604800, "weeks": 604800}
	multiplier := multipliers[normalized]
	if multiplier == 0 {
		multiplier = 60
	}
	return duration * multiplier
}

func resetSeconds(objects ...map[string]any) (int64, bool) {
	for _, object := range objects {
		if value, ok := floatValue(object, "reset_in", "resetIn", "ttl"); ok && value >= 0 {
			return int64(value), true
		}
	}
	return 0, false
}

func windowLabel(seconds int64) string {
	switch seconds {
	case 18000:
		return "5-hour limit"
	case 604800:
		return "Weekly limit"
	case 2592000:
		return "Monthly limit"
	}
	return ""
}
func usdValue(object map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		if nested := objectMap(object, key); nested != nil {
			value, ok := floatValue(nested, "val")
			return value / 100, ok
		}
		if value, ok := floatValue(object, key); ok {
			return value / 100, true
		}
	}
	return 0, false
}
func xaiResetAt(config map[string]any) string {
	return quotaResetAt(
		firstString(objectMap(config, "currentPeriod", "current_period"), "end"),
		firstString(config, "billingPeriodEnd", "billing_period_end"),
	)
}
