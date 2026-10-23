// Package moderation implements synchronous, text-only request admission checks.
package moderation

import (
	"cpa-codex-controls/internal/messages"
	"maps"
	"math"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const MaxTextRunes = 64000
const MaxBodyBytes = 4 << 20

type Rule struct {
	ID      string `json:"id" yaml:"id"`
	Name    string `json:"name" yaml:"name"`
	Kind    string `json:"kind" yaml:"kind"`
	Pattern string `json:"pattern" yaml:"pattern"`
}

type Policy struct {
	Mode          string   `json:"mode" yaml:"mode"`
	Strategy      string   `json:"strategy" yaml:"strategy"`
	Scope         string   `json:"scope" yaml:"scope"`
	ModelMode     string   `json:"model_mode" yaml:"model_mode"`
	Models        []string `json:"models" yaml:"models"`
	Rules         []Rule   `json:"rules" yaml:"rules"`
	FailureAction string   `json:"failure_action" yaml:"failure_action"`
	LogExcerpt    bool     `json:"log_excerpt" yaml:"log_excerpt"`
}

// API credentials are resolved from the CPA process environment. Config and
// management responses never contain the secret itself.
type API struct {
	URL         string             `json:"url" yaml:"url"`
	Model       string             `json:"model" yaml:"model"`
	KeyEnv      string             `json:"key_env" yaml:"key_env"`
	TimeoutMS   int                `json:"timeout_ms" yaml:"timeout_ms"`
	MaxInFlight int                `json:"max_in_flight" yaml:"max_in_flight"`
	Thresholds  map[string]float64 `json:"thresholds" yaml:"thresholds"`
}

type Config struct {
	Policy `yaml:",inline"`
	API    API `json:"api" yaml:"api"`
}

type Override struct {
	Mode   string  `json:"mode" yaml:"mode"`
	Policy *Policy `json:"policy,omitempty" yaml:"policy,omitempty"`
}

type Compiled struct {
	Policy   Policy
	patterns []*regexp.Regexp
}

func DefaultPolicy() Policy {
	return Policy{Mode: "off", Strategy: "keywords", Scope: "latest_user", ModelMode: "all", Models: []string{}, Rules: []Rule{}, FailureAction: "block"}
}
func DefaultConfig() Config {
	return Config{Policy: DefaultPolicy(), API: API{Model: "omni-moderation-latest", KeyEnv: "CPA_POLICY_MODERATION_API_KEY", TimeoutMS: 3000, MaxInFlight: 8, Thresholds: map[string]float64{}}}
}

func (p Policy) Clone() Policy {
	p.Rules = slices.Clone(p.Rules)
	p.Models = slices.Clone(p.Models)
	return p
}
func (c Config) Clone() Config {
	c.Policy = c.Policy.Clone()
	c.API.Thresholds = maps.Clone(c.API.Thresholds)
	return c
}
func (o *Override) Clone() *Override {
	if o == nil {
		return nil
	}
	copy := *o
	if o.Policy != nil {
		p := o.Policy.Clone()
		copy.Policy = &p
	}
	return &copy
}

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
var categoryName = regexp.MustCompile(`^[a-z][a-z0-9_/-]{0,79}$`)

func (p Policy) Compile() (*Compiled, error) {
	if p.Mode == "" {
		p.Mode = "off"
	}
	if p.Strategy == "" {
		p.Strategy = "keywords"
	}
	if p.Scope == "" {
		p.Scope = "latest_user"
	}
	if p.ModelMode == "" {
		p.ModelMode = "all"
	}
	if p.FailureAction == "" {
		p.FailureAction = "block"
	}
	if !slices.Contains([]string{"off", "observe", "pre_block"}, p.Mode) {
		return nil, messages.Errorf("moderation mode must be off, observe, or pre_block")
	}
	if !slices.Contains([]string{"keywords", "api", "keywords_and_api"}, p.Strategy) {
		return nil, messages.Errorf("moderation strategy must be keywords, api, or keywords_and_api")
	}
	if !slices.Contains([]string{"latest_user", "all_users"}, p.Scope) {
		return nil, messages.Errorf("moderation scope must be latest_user or all_users")
	}
	if !slices.Contains([]string{"all", "include", "exclude"}, p.ModelMode) {
		return nil, messages.Errorf("moderation model_mode must be all, include, or exclude")
	}
	if p.FailureAction != "allow" && p.FailureAction != "block" {
		return nil, messages.Errorf("moderation failure_action must be allow or block")
	}
	if len(p.Rules) > 100 || len(p.Models) > 100 {
		return nil, messages.Errorf("moderation supports at most 100 rules and 100 model names")
	}
	p = p.Clone()
	if p.Rules == nil {
		p.Rules = []Rule{}
	}
	if p.Models == nil {
		p.Models = []string{}
	}
	for i, model := range p.Models {
		p.Models[i] = strings.TrimSpace(model)
		if p.Models[i] == "" || len(p.Models[i]) > 256 {
			return nil, messages.Errorf("moderation model names must contain 1–256 bytes")
		}
	}
	if p.ModelMode != "all" && len(p.Models) == 0 {
		return nil, messages.Errorf("moderation model filter requires at least one model name")
	}
	c := &Compiled{Policy: p}
	seen := map[string]bool{}
	for i, rule := range p.Rules {
		if !identifier.MatchString(rule.ID) || seen[rule.ID] {
			return nil, messages.Errorf("moderation rule %d requires a unique identifier", i+1)
		}
		seen[rule.ID] = true
		if len(rule.Name) > 128 || len(rule.Pattern) > 512 || strings.TrimSpace(rule.Pattern) == "" {
			return nil, messages.Errorf("moderation rule %d has an invalid name or pattern (maximum 128 / 512 bytes)", i+1)
		}
		pattern := rule.Pattern
		switch rule.Kind {
		case "keyword":
			pattern = "(?i)" + regexp.QuoteMeta(pattern)
		case "regex":
		default:
			return nil, messages.Errorf("moderation rule %d kind must be keyword or regex", i+1)
		}
		r, err := regexp.Compile(pattern)
		if err != nil {
			return nil, messages.Errorf("moderation rule %d contains an invalid regular expression", i+1)
		}
		if r.MatchString("") {
			return nil, messages.Errorf("moderation rule %d must not match empty text", i+1)
		}
		c.patterns = append(c.patterns, r)
	}
	if p.Mode != "off" && p.Strategy == "keywords" && len(p.Rules) == 0 {
		return nil, messages.Errorf("keyword moderation requires at least one rule")
	}
	return c, nil
}

func (o *Override) Normalize() (*Override, error) {
	if o == nil || o.Mode == "" || o.Mode == "inherit" {
		return nil, nil
	}
	if o.Mode == "off" {
		return &Override{Mode: "off"}, nil
	}
	if o.Mode != "custom" || o.Policy == nil {
		return nil, messages.Errorf("moderation override requires inherit, off, or custom with a policy")
	}
	p, err := o.Policy.Compile()
	if err != nil {
		return nil, err
	}
	return &Override{Mode: "custom", Policy: &p.Policy}, nil
}

func (c Config) Normalize() (Config, error) {
	p, err := c.Policy.Compile()
	if err != nil {
		return c, err
	}
	c.Policy = p.Policy
	c.API.URL = strings.TrimSpace(c.API.URL)
	c.API.Model = strings.TrimSpace(c.API.Model)
	c.API.KeyEnv = strings.TrimSpace(c.API.KeyEnv)
	if c.API.TimeoutMS == 0 {
		c.API.TimeoutMS = 3000
	}
	if c.API.MaxInFlight == 0 {
		c.API.MaxInFlight = 8
	}
	if c.API.TimeoutMS < 100 || c.API.TimeoutMS > 10000 {
		return c, messages.Errorf("moderation API timeout_ms must be between 100 and 10000")
	}
	if c.API.MaxInFlight < 1 || c.API.MaxInFlight > 64 {
		return c, messages.Errorf("moderation API max_in_flight must be between 1 and 64")
	}
	if c.API.KeyEnv != "" && !envName.MatchString(c.API.KeyEnv) {
		return c, messages.Errorf("moderation API key_env must be an environment variable name")
	}
	if len(c.API.Model) > 128 {
		return c, messages.Errorf("moderation API model must not exceed 128 bytes")
	}
	if c.API.URL != "" {
		u, parseErr := url.Parse(c.API.URL)
		if parseErr != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(c.API.URL) > 2048 {
			return c, messages.Errorf("moderation API URL must be an absolute URL without credentials, query, or fragment")
		}
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
			return c, messages.Errorf("moderation API URL requires HTTPS (HTTP is allowed only on loopback)")
		}
	}
	if c.API.Thresholds == nil {
		c.API.Thresholds = map[string]float64{}
	} else {
		c.API.Thresholds = maps.Clone(c.API.Thresholds)
	}
	if len(c.API.Thresholds) > 40 {
		return c, messages.Errorf("moderation supports at most 40 category thresholds")
	}
	for key, value := range c.API.Thresholds {
		if !categoryName.MatchString(key) || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return c, messages.Errorf("moderation API thresholds require category names and scores from 0 to 1")
		}
	}
	if err := ValidateAPIUse(c.Policy, c.API); err != nil {
		return c, err
	}
	return c, nil
}

func ValidateAPIUse(p Policy, api API) error {
	if p.Mode != "off" && p.Strategy != "keywords" && (api.URL == "" || api.Model == "") {
		return messages.Errorf("moderation API URL and model are required for this policy")
	}
	return nil
}

func (c *Compiled) Applies(model string) bool {
	if c == nil || c.Policy.Mode == "off" {
		return false
	}
	found := slices.Contains(c.Policy.Models, model)
	return c.Policy.ModelMode == "all" || c.Policy.ModelMode == "include" && found || c.Policy.ModelMode == "exclude" && !found
}
