package settings

import (
	"encoding/json"
	"regexp"
	"strings"
)

var sensitiveAssignment = regexp.MustCompile(`(?i)(\b(?:authorization|api[-_]?key|access[-_]?token|refresh[-_]?token|id[-_]?token|client[-_]?secret|password)\b["']?\s*[:=]\s*["']?)(?:bearer\s+)?[^\s"',;&}]+`)
var bearerCredential = regexp.MustCompile(`(?i)\bbearer\s+[^\s"',;]+`)
var standardAPIKey = regexp.MustCompile(`\bsk-[a-zA-Z0-9_-]+`)

func RedactText(value string) string {
	var payload any
	if json.Unmarshal([]byte(value), &payload) == nil {
		redactJSON(payload)
		if raw, err := json.Marshal(payload); err == nil {
			value = string(raw)
		}
	}
	value = sensitiveAssignment.ReplaceAllString(value, "${1}[redacted]")
	value = bearerCredential.ReplaceAllString(value, "Bearer [redacted]")
	return standardAPIKey.ReplaceAllString(value, "[redacted]")
}
func redactJSON(value any) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			switch strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key)) {
			case "authorization", "apikey", "accesstoken", "refreshtoken", "idtoken", "clientsecret", "password":
				value[key] = "[redacted]"
			default:
				redactJSON(child)
			}
		}
	case []any:
		for _, child := range value {
			redactJSON(child)
		}
	}
}
