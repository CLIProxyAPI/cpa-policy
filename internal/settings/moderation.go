package settings

import (
	"cpa-codex-controls/internal/moderation"
	"fmt"
	"regexp"
	"strings"
	"time"
)

func normalizeModeration(cfg *Config) error {
	c, err := cfg.Moderation.Normalize()
	if err != nil {
		return fmt.Errorf("moderation: %w", err)
	}
	for _, account := range cfg.CredentialPolicies {
		if account.Moderation != nil && account.Moderation.Policy != nil {
			if err := moderation.ValidateAPIUse(*account.Moderation.Policy, c.API); err != nil {
				return err
			}
		}
	}
	cfg.Moderation = c
	return nil
}

func (s *Store) ModerationConfig() moderation.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Moderation.Clone()
}

// Returned compiled policies are immutable; account selection and global API
// settings are captured under the same lock during hot reconfiguration.
func (s *Store) ModerationSnapshot(index string) (*moderation.Compiled, moderation.API, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.moderationGlobal
	if account, ok := s.moderationAccounts[index]; ok {
		p = account
	}
	return p, s.cfg.Moderation.API, len(s.moderationAccounts) > 0
}

type ModerationEvent struct {
	At           time.Time `json:"at"`
	AuthIndex    string    `json:"auth_index,omitempty"`
	Model        string    `json:"model,omitempty"`
	SourceFormat string    `json:"source_format,omitempty"`
	moderation.Result
	Excerpt string `json:"excerpt,omitempty"`
}

var emailAddress = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
var phoneNumber = regexp.MustCompile(`\+?\d[\d ()-]{7,}\d`)
var tokenLike = regexp.MustCompile(`\b[A-Za-z0-9_-]{24,}(?:\.[A-Za-z0-9_-]+)*\b`)

func ModerationExcerpt(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	text = RedactText(text)
	text = emailAddress.ReplaceAllString(text, "[email]")
	text = phoneNumber.ReplaceAllString(text, "[phone]")
	text = tokenLike.ReplaceAllString(text, "[token]")
	runes := []rune(text)
	if len(runes) > 240 {
		return string(runes[:240]) + "…"
	}
	return text
}

func (s *Store) AddModeration(event ModerationEvent) error {
	event.Text = ""
	event.Model = ModerationExcerpt(event.Model)
	event.AuthIndex = ModerationExcerpt(event.AuthIndex)
	event.SourceFormat = ModerationExcerpt(event.SourceFormat)
	event.Excerpt = ModerationExcerpt(event.Excerpt)
	// Copy before redaction: the caller may still be using its result.
	names := make([]string, len(event.RuleNames))
	for i, name := range event.RuleNames {
		names[i] = ModerationExcerpt(name)
	}
	event.RuleNames = names
	return s.append("moderation", "", event, event.At)
}
