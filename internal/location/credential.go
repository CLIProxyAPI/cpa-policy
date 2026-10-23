package location

import (
	"cpa-codex-controls/internal/moderation"
	"fmt"
	"regexp"
)

// CredentialPolicy is keyed by the host's stable, opaque auth_index (never a key).
// A compatible provider with several keys has an independent policy per key row.
type CredentialPolicy struct {
	MaxConcurrency int `json:"max_concurrency" yaml:"max_concurrency"`
	// Retired field read for migration and removed by Normalize.
	ConcurrencyMode string               `json:"concurrency_mode,omitempty" yaml:"concurrency_mode,omitempty"`
	LocationMode    string               `json:"location_mode" yaml:"location_mode"`
	Location        Location             `json:"location" yaml:"location"`
	Moderation      *moderation.Override `json:"moderation,omitempty" yaml:"moderation,omitempty"`
}

type CredentialPolicies map[string]CredentialPolicy

var credentialIndexPattern = regexp.MustCompile(`^[A-Za-z0-9_:-]{1,128}$`)

func (p CredentialPolicies) Normalize() (CredentialPolicies, error) {
	if len(p) > 10000 {
		return nil, fmt.Errorf("at most 10000 credential policies are supported")
	}
	out := make(CredentialPolicies, len(p))
	for index, rule := range p {
		if !credentialIndexPattern.MatchString(index) {
			return nil, fmt.Errorf("invalid credential index")
		}
		if rule.MaxConcurrency < 0 || rule.MaxConcurrency > 100000 {
			return nil, fmt.Errorf("max_concurrency must be between 0 and 100000")
		}
		// Old inherited limits become unlimited. Explicit and legacy numeric
		// limits retain their meaning; normalized policies have no mode field.
		switch rule.ConcurrencyMode {
		case "", "custom":
		case "inherit":
			rule.MaxConcurrency = 0
		default:
			return nil, fmt.Errorf("concurrency_mode must be inherit or custom")
		}
		rule.ConcurrencyMode = ""
		if rule.LocationMode == "" {
			rule.LocationMode = "inherit"
		}
		switch rule.LocationMode {
		case "inherit", "preserve":
		case "custom":
			compiled, err := Compile(rule.Location)
			if err != nil {
				return nil, err
			}
			rule.Location = compiled.Location
		default:
			return nil, fmt.Errorf("location_mode must be inherit, custom, or preserve")
		}
		override, err := rule.Moderation.Normalize()
		if err != nil {
			return nil, err
		}
		rule.Moderation = override
		out[index] = rule
	}
	return out, nil
}

func (p CredentialPolicies) Clone() CredentialPolicies {
	out := make(CredentialPolicies, len(p))
	for index, policy := range p {
		policy.Moderation = policy.Moderation.Clone()
		out[index] = policy
	}
	return out
}
