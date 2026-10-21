package plugin

import (
	"testing"

	"cpa-codex-controls/internal/settings"
)

func TestUsageFailureDetails(t *testing.T) {
	const closure = "websocket: close 1006 (abnormal closure): unexpected EOF"
	const canceled = `Post "https://api.deepseek.com/anthropic/v1/messages?beta=true": context canceled`
	for _, test := range []struct {
		name       string
		statusCode int
		body       string
		want       settings.ErrorDetail
	}{{
		name: "payload is compacted and its code names the failure", statusCode: 429,
		body: "{\n  \"error\": { \"message\": \"rate limited\", \"type\": \"rate_limit_error\", \"code\": \"slow_down\" },\n  \"request_id\": \"req_42\"\n}",
		want: settings.ErrorDetail{StatusCode: 429, ErrorType: "slow_down",
			Body: `{"error":{"message":"rate limited","type":"rate_limit_error","code":"slow_down"},"request_id":"req_42"}`},
	}, {
		name: "payload without a code falls back to its type", body: `{"error":{"message":"boom","type":"server_error"}}`,
		want: settings.ErrorDetail{ErrorType: "server_error", Body: `{"error":{"message":"boom","type":"server_error"}}`},
	}, {
		name: "payload without a category is classified from its text", body: `{"error":{"message":"` + closure + `"}}`,
		want: settings.ErrorDetail{ErrorType: "websocket_abnormal_closure", Body: `{"error":{"message":"` + closure + `"}}`},
	}, {
		name: "transport error is stored as written", statusCode: 502, body: closure,
		want: settings.ErrorDetail{StatusCode: 502, ErrorType: "websocket_abnormal_closure", Body: closure},
	}, {
		name: "json-encoded body is unwrapped", body: `"Post \"https://api.deepseek.com/anthropic/v1/messages?beta=true\": context canceled"`,
		want: settings.ErrorDetail{ErrorType: "context_canceled", Body: canceled},
	}, {
		name: "bare detail payload is classified from its text", statusCode: 429, body: `{"detail":"Rate limit exceeded"}`,
		want: settings.ErrorDetail{StatusCode: 429, ErrorType: "rate_limit_exceeded", Body: `{"detail":"Rate limit exceeded"}`},
	}, {
		name: "unknown signature stays unclassified", statusCode: 308, body: "redirect failed",
		want: settings.ErrorDetail{StatusCode: 308, Body: "redirect failed"},
	}} {
		t.Run(test.name, func(t *testing.T) {
			if got := usageFailureDetails(UsageFailure{StatusCode: test.statusCode, Body: test.body}); got != test.want {
				t.Errorf("details = %+v, want %+v", got, test.want)
			}
		})
	}
}
