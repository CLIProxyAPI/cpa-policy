package settings

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"cpa-codex-controls/internal/location"
	"gopkg.in/yaml.v3"
)

const DefaultDiagnosticsFile = "plugins/cpa-codex-controls-diagnostics.db"

// Legacy diagnostic imports require an explicit state_file; independent installs
// must not open another plugin's database.
const DefaultLegacyStateFile = ""

type Config struct {
	Enabled            bool                        `yaml:"enabled"`
	Debug              bool                        `yaml:"debug"`
	DiagnosticsFile    string                      `yaml:"diagnostics_file"`
	LegacyStateFile    string                      `yaml:"state_file,omitempty"`
	CustomLocation     location.Settings           `yaml:"custom_location"`
	Concurrency        ConcurrencySettings         `yaml:"concurrency"`
	CredentialPolicies location.CredentialPolicies `yaml:"credential_policies"`
}

func DefaultConfig() Config {
	return Config{DiagnosticsFile: DefaultDiagnosticsFile, LegacyStateFile: DefaultLegacyStateFile, Concurrency: DefaultConcurrencySettings()}
}

func DecodeConfig(raw []byte) (Config, error) {
	// Ignore only known retired fields so upgrades can retain their host config.
	// state_file is used solely to import diagnostic history without modifying it.
	document := struct {
		Config         `yaml:",inline"`
		Priority       yaml.Node `yaml:"priority"`
		Store          yaml.Node `yaml:"store"`
		Routing        yaml.Node `yaml:"routing"`
		ProxyLocations yaml.Node `yaml:"proxy_locations"`
		FastBilling    yaml.Node `yaml:"codex_fast_mode_billing"`
		MaskEmails     yaml.Node `yaml:"mask_api_key_view_emails"`
		AllowReset     yaml.Node `yaml:"allow_api_key_quota_reset"`
	}{Config: DefaultConfig()}
	if len(bytes.TrimSpace(raw)) > 0 {
		decoder := yaml.NewDecoder(bytes.NewReader(raw))
		decoder.KnownFields(true)
		if err := decoder.Decode(&document); err != nil {
			return Config{}, fmt.Errorf("Parse plugin configuration: %w", err)
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return Config{}, fmt.Errorf("Plugin configuration must contain exactly one YAML document")
		}
	}
	cfg := document.Config
	concurrency, err := cfg.Concurrency.Normalize()
	if err != nil {
		return Config{}, fmt.Errorf("concurrency: %w", err)
	}
	cfg.Concurrency = concurrency
	policies, err := cfg.CredentialPolicies.Normalize()
	if err != nil {
		return Config{}, fmt.Errorf("credential_policies: %w", err)
	}
	cfg.CredentialPolicies = policies
	compiled, err := cfg.CustomLocation.Compile()
	if err != nil {
		return Config{}, fmt.Errorf("custom_location: %w", err)
	}
	if compiled != nil {
		cfg.CustomLocation.Location = compiled.Location
	}
	cfg.DiagnosticsFile = strings.TrimSpace(cfg.DiagnosticsFile)
	if cfg.DiagnosticsFile == "" {
		cfg.DiagnosticsFile = DefaultDiagnosticsFile
	}
	return cfg, nil
}
