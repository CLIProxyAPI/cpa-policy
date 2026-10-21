package settings

import (
	"encoding/json"
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestConcurrencySettingsDefaultsAndValidation(t *testing.T) {
	cfg, err := DecodeConfig([]byte("enabled: true\n"))
	if err != nil || cfg.Concurrency != DefaultConcurrencySettings() {
		t.Fatalf("defaults: %+v %v", cfg.Concurrency, err)
	}
	for _, raw := range []string{
		"concurrency:\n  account_queue_capacity: 1001\n",
		"concurrency:\n  queue_timeout_seconds: 0\n",
		"concurrency:\n  queue_timeout_seconds: 121\n",
		"credential_policies:\n  one:\n    concurrency_mode: bad\n",
	} {
		if _, err := DecodeConfig([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	cfg, err = DecodeConfig([]byte("concurrency:\n  account_queue_capacity: 2\n"))
	if err != nil || cfg.Concurrency.QueueTimeoutSeconds != 30 {
		t.Fatalf("partial config defaults: %+v %v", cfg, err)
	}
}

func TestRetiredConcurrencySettingsMigrateToPerAccountLimits(t *testing.T) {
	cfg, err := DecodeConfig([]byte(`concurrency:
  default_max_concurrency: 1
  account_queue_capacity: 2
credential_policies:
  inherited:
    max_concurrency: 7
    concurrency_mode: inherit
  custom:
    max_concurrency: 2
    concurrency_mode: custom
  legacy:
    max_concurrency: 3
  zero:
    max_concurrency: 0
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Concurrency.DefaultMaxConcurrency != 0 || cfg.Concurrency.AccountQueueCapacity != 2 || cfg.Concurrency.QueueTimeoutSeconds != 30 {
		t.Fatalf("retired default not discarded: %+v", cfg.Concurrency)
	}
	for index, want := range map[string]int{"inherited": 0, "custom": 2, "legacy": 3, "zero": 0, "missing": 0} {
		if got := cfg.CredentialPolicies[index]; got.MaxConcurrency != want || got.ConcurrencyMode != "" {
			t.Fatalf("policy %s: %+v, want %d", index, got, want)
		}
	}
	for _, marshal := range []func(any) ([]byte, error){json.Marshal, yaml.Marshal} {
		raw, err := marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "default_max_concurrency") || strings.Contains(string(raw), "concurrency_mode") {
			t.Fatalf("retired settings were emitted: %s", raw)
		}
	}
}
