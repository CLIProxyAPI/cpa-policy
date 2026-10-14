package plugin

import (
	"bytes"
	"encoding/json"
	"strings"

	"cpa-key-billing/internal/billing"
)

func usageFailureDetails(failure UsageFailure) billing.RequestError {
	body := normalizeFailureBody(failure.Body)
	return billing.RequestError{
		StatusCode: validFailureStatus(failure.StatusCode),
		ErrorType:  failureErrorType(body),
		Body:       body,
	}
}

// A body may arrive as a JSON-encoded string, possibly nested; unwrap it so
// the stored text carries no transport escaping.
func normalizeFailureBody(body string) string {
	body = strings.TrimSpace(body)
	for {
		var unwrapped string
		if json.Unmarshal([]byte(body), &unwrapped) != nil {
			break
		}
		body = strings.TrimSpace(unwrapped)
	}
	var compact bytes.Buffer
	if json.Compact(&compact, []byte(body)) == nil {
		return compact.String()
	}
	return body
}

func failureErrorType(body string) string {
	code, errorType := parseUpstreamFailure(body)
	switch {
	case code != "":
		return code
	case errorType != "":
		return errorType
	default:
		return inferredFailureType(body)
	}
}

func inferredFailureType(body string) string {
	body = strings.ToLower(body)
	switch {
	case strings.Contains(body, "websocket: close 1006"):
		return "websocket_abnormal_closure"
	case strings.Contains(body, "context canceled"):
		return "context_canceled"
	case strings.Contains(body, "rate limit exceeded"):
		return "rate_limit_exceeded"
	case strings.Contains(body, "stream disconnected before completion") ||
		strings.Contains(body, "websocket: close ") ||
		strings.Contains(body, "use of closed network connection") ||
		strings.Contains(body, "upstream connect error or disconnect/reset before headers") ||
		strings.Contains(body, "context deadline exceeded") ||
		strings.Contains(body, "tls handshake") ||
		strings.Contains(body, "read tcp ") || strings.Contains(body, "write tcp ") ||
		strings.Contains(body, "dial tcp ") || strings.Contains(body, "dial udp ") ||
		strings.Contains(body, "connection reset by peer") || strings.Contains(body, "connection refused") ||
		strings.Contains(body, "broken pipe") || strings.Contains(body, "network is unreachable") ||
		strings.Contains(body, "no route to host") || strings.Contains(body, "no such host") ||
		strings.Contains(body, "protocol_error; received from peer"):
		return "network_error"
	default:
		return ""
	}
}

func parseUpstreamFailure(body string) (code, errorType string) {
	if start := strings.IndexByte(body, '{'); start > 0 {
		body = body[start:]
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var root map[string]any
	if decoder.Decode(&root) != nil || root == nil {
		return "", ""
	}
	node := root
	for _, path := range [][]string{{"error"}, {"response", "error"}, {"body", "error"}} {
		if found := failureObjectAt(root, path...); found != nil {
			node = found
			break
		}
	}
	errorType = failureString(node["type"])
	if strings.EqualFold(errorType, "error") {
		// The generic wrapper around a nested error object, not a category.
		errorType = ""
	}
	return failureString(node["code"]), errorType
}

func failureObjectAt(root map[string]any, path ...string) map[string]any {
	var current any = root
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[key]
	}
	object, _ := current.(map[string]any)
	return object
}

func failureString(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

func validFailureStatus(status int) int {
	if status < 100 || status > 599 {
		return 0
	}
	return status
}
