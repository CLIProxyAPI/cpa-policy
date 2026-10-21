package plugin

import (
	"strings"
	"time"
)

type codexLimit struct{ key, labelPrefix string }

var codexMainLimit = codexLimit{key: "codex"}
var codexCodeReviewLimit = codexLimit{key: "code-review", labelPrefix: "Code Review "}

const claudeUnifiedLimit = "unified"

func codexAdditionalLimit(name string) codexLimit {
	if name == "" {
		return codexLimit{key: "additional", labelPrefix: "Additional "}
	}
	return codexLimit{key: name, labelPrefix: name + " "}
}
func periodSeconds(start, resetAt string) int64 {
	from, errFrom := time.Parse(time.RFC3339, strings.TrimSpace(start))
	to, errTo := time.Parse(time.RFC3339, resetAt)
	if errFrom != nil || errTo != nil || !to.After(from) {
		return 0
	}
	return int64(to.Sub(from) / time.Second)
}
