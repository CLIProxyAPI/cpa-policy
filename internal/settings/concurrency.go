package settings

import (
	"cpa-codex-controls/internal/location"
	"fmt"
	"maps"
)

// ConcurrencySettings control waiting only for credentials with a positive limit.
type ConcurrencySettings struct {
	// Retired field accepted only to migrate existing configuration. Never enforced.
	DefaultMaxConcurrency int `json:"default_max_concurrency,omitempty" yaml:"default_max_concurrency,omitempty"`
	AccountQueueCapacity  int `json:"account_queue_capacity" yaml:"account_queue_capacity"`
	QueueTimeoutSeconds   int `json:"queue_timeout_seconds" yaml:"queue_timeout_seconds"`
}

func DefaultConcurrencySettings() ConcurrencySettings {
	return ConcurrencySettings{QueueTimeoutSeconds: 30}
}

func (c ConcurrencySettings) Normalize() (ConcurrencySettings, error) {
	c.DefaultMaxConcurrency = 0
	if c.AccountQueueCapacity < 0 || c.AccountQueueCapacity > 1000 {
		return c, fmt.Errorf("account_queue_capacity must be between 0 and 1000")
	}
	if c.QueueTimeoutSeconds < 1 || c.QueueTimeoutSeconds > 120 {
		return c, fmt.Errorf("queue_timeout_seconds must be between 1 and 120")
	}
	return c, nil
}

func (s *Store) ConcurrencySettings() ConcurrencySettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Concurrency
}

func (s *Store) CredentialConcurrencyView() (location.CredentialPolicies, ConcurrencySettings) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.cfg.CredentialPolicies), s.cfg.Concurrency
}

// Read limit and queue settings under one lock so a reload cannot mix versions.
func (s *Store) ConcurrencySnapshot(index string) (ConcurrencySettings, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Concurrency, s.cfg.CredentialPolicies[index].MaxConcurrency
}
