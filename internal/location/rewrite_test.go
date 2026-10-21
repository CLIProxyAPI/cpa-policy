package location

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func testLocation(t *testing.T, zone string) *Compiled {
	t.Helper()
	value, err := Compile(Location{Country: "us", Region: "California", City: "Los Angeles", Timezone: zone})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRewriteContextsAndSearchWithoutTouchingOtherFields(t *testing.T) {
	environment := "<environment_context>\n<cwd>/tmp/项目</cwd><current_date>2000-01-01</current_date><timezone>Asia/Shanghai</timezone><other><timezone>keep</timezone></other>\n</environment_context>"
	desktop := " <codex_apps_client_time_context><timezone>UTC</timezone><current_date>2000-01-01</current_date>Keep instructions.</codex_apps_client_time_context>\n"
	payload := map[string]any{
		"input": []any{
			map[string]any{"role": "user", "content": []any{map[string]string{"type": "input_text", "text": environment}}},
			map[string]any{"role": "developer", "content": []any{map[string]string{"type": "input_text", "text": desktop}}},
		},
		"tools":  []any{map[string]string{"type": "web_search_preview"}, map[string]string{"type": "function", "name": "timezone"}},
		"opaque": json.RawMessage(`{"integer":9007199254740993,"timestamp":1789293131.822,"text":"UTC"}`),
	}
	body, _ := json.Marshal(payload)
	now := time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	got, err := Rewrite(body, testLocation(t, "America/Los_Angeles"), now)
	if err != nil {
		t.Fatal(err)
	}
	for i, original := range []string{environment, desktop} {
		want := strings.ReplaceAll(original, "2000-01-01", "2026-10-19")
		want = strings.ReplaceAll(want, ">Asia/Shanghai<", ">America/Los_Angeles<")
		want = strings.ReplaceAll(want, ">UTC<", ">America/Los_Angeles<")
		path := []string{"input.0.content.0.text", "input.1.content.0.text"}[i]
		if gjson.GetBytes(got, path).String() != want {
			t.Fatalf("%s: %s", path, got)
		}
	}
	if gjson.GetBytes(got, "tools.0.user_location.timezone").String() != "America/Los_Angeles" || gjson.GetBytes(got, "tools.0.user_location.country").String() != "US" {
		t.Fatalf("missing search location: %s", got)
	}
	for _, path := range []string{"opaque", "tools.1"} {
		if gjson.GetBytes(body, path).Raw != gjson.GetBytes(got, path).Raw {
			t.Fatalf("changed %s", path)
		}
	}
	again, err := Rewrite(got, testLocation(t, "America/Los_Angeles"), now)
	if err != nil || !bytes.Equal(got, again) {
		t.Fatalf("not idempotent: %s", again)
	}
}

func TestDefaultIsByteForBytePassThrough(t *testing.T) {
	for _, body := range []string{"", "invalid", `  { "input": [], "tools": [{"type":"web_search"}] }  `} {
		got, err := Rewrite([]byte(body), nil, time.Now())
		if err != nil || string(got) != body {
			t.Fatalf("default modified %q", body)
		}
	}
}

func TestContextRecognitionIsConservative(t *testing.T) {
	env := "<environment_context><timezone>UTC</timezone></environment_context>"
	for _, tc := range []struct{ name, role, text, partType, itemType, kinds string }{
		{"ordinary text", "user", "Example: " + env, "input_text", "message", ""},
		{"suffix", "user", env + " tail", "input_text", "message", ""},
		{"assistant", "assistant", env, "input_text", "message", ""},
		{"wrong role", "developer", env, "input_text", "message", ""},
		{"tool result", "user", env, "input_text", "function_call_output", ""},
		{"classified text", "user", env, "input_text", "message", `["user.text"]`},
		{"empty classification", "user", env, "input_text", "message", `[]`},
		{"wrong content type", "user", env, "output_text", "message", ""},
		{"broken XML", "user", "<environment_context><timezone>UTC</environment_context>", "input_text", "message", ""},
		{"nested", "user", "<environment_context><example><timezone>UTC</timezone></example></environment_context>", "input_text", "message", ""},
		{"two roots", "user", env + env, "input_text", "message", ""},
		{"DTD", "user", "<environment_context><!DOCTYPE test><timezone>UTC</timezone></environment_context>", "input_text", "message", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := map[string]any{"type": tc.itemType, "role": tc.role, "content": []any{map[string]string{"type": tc.partType, "text": tc.text}}}
			if tc.kinds != "" {
				item["internal_chat_message_metadata_passthrough"] = map[string]any{"content_item_kinds": json.RawMessage(tc.kinds)}
			}
			body, _ := json.Marshal(map[string]any{"input": []any{item}})
			got, err := Rewrite(body, testLocation(t, "America/New_York"), time.Now())
			if err != nil || !bytes.Equal(body, got) {
				t.Fatalf("unexpected rewrite: %s", got)
			}
		})
	}
}

func TestDateUsesTimezoneAndDST(t *testing.T) {
	for _, tc := range []struct{ zone, utc, date string }{
		{"America/New_York", "2026-03-08T04:30:00Z", "2026-03-07"},
		{"America/New_York", "2026-03-09T04:30:00Z", "2026-03-09"},
		{"America/New_York", "2026-11-02T04:30:00Z", "2026-11-01"},
		{"Pacific/Kiritimati", "2026-10-20T12:00:00Z", "2026-10-21"},
		{"Asia/Kathmandu", "2026-10-20T18:30:00Z", "2026-10-21"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.utc)
		if got := testLocation(t, tc.zone).Date(now); got != tc.date {
			t.Fatalf("%s: %s != %s", tc.zone, got, tc.date)
		}
	}
}

func TestLocationValidation(t *testing.T) {
	if got, err := (Settings{}).Compile(); err != nil || got != nil {
		t.Fatal("default must be disabled")
	}
	for _, value := range []Location{
		{}, {Country: "USA", Region: "X", City: "Y", Timezone: "UTC"},
		{Country: "US", Region: "X", City: "Y", Timezone: "Local"},
		{Country: "US", Region: "X", City: "Y", Timezone: "Not/AZone"},
		{Country: "US", Region: "X\nY", City: "Y", Timezone: "UTC"},
	} {
		if _, err := Compile(value); err == nil {
			t.Fatalf("accepted %+v", value)
		}
	}
}

func FuzzRewrite(f *testing.F) {
	f.Add(`{"input":[{"role":"user","content":[{"type":"input_text","text":"<environment_context><timezone>UTC</timezone></environment_context>"}]}]}`)
	f.Add(`{"tools":[{"type":"web_search"}]}`)
	configured, _ := Compile(Location{Country: "US", Region: "California", City: "Los Angeles", Timezone: "America/Los_Angeles"})
	f.Fuzz(func(t *testing.T, input string) {
		got, err := Rewrite([]byte(input), configured, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		if json.Valid([]byte(input)) && !json.Valid(got) {
			t.Fatal("produced invalid JSON")
		}
	})
}
