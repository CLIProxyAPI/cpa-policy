package moderation

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

type Input struct {
	Text   string
	Media  bool
	Reason string
}

// Extract reads only user-authored text, never tool arguments/results, system
// instructions, assistant history, image URLs, or file bodies. History scope is
// explicit, and oversize input is an error rather than a silently audited prefix.
func Extract(format string, body []byte, scope string) Input {
	if len(body) > MaxBodyBytes {
		return Input{Reason: "body_too_large"}
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil {
		return Input{Reason: "invalid_body"}
	}
	format = strings.ToLower(strings.TrimSpace(format))
	var raw json.RawMessage
	gemini := false
	switch format {
	case "openai", "openai-chat", "chat-completions", "claude", "anthropic":
		raw = root["messages"]
	case "codex", "openai-response", "openai-responses", "responses":
		raw = root["input"]
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return boundedInput(text, false)
		}
	case "gemini", "vertex", "vertex-ai":
		raw, gemini = root["contents"], true
	default:
		return Input{Reason: "unsupported_format"}
	}
	var messages []struct {
		Role    string          `json:"role"`
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		Parts   json.RawMessage `json:"parts"`
	}
	if len(raw) == 0 {
		return Input{Reason: "no_user_text"}
	}
	if json.Unmarshal(raw, &messages) != nil {
		return Input{Reason: "invalid_body"}
	}
	texts := []string{}
	media := false
	for _, message := range messages {
		if message.Role != "user" || message.Type != "" && message.Type != "message" {
			continue
		}
		content := message.Content
		if gemini {
			content = message.Parts
		}
		text, hasMedia := userText(content, gemini)
		if scope == "latest_user" {
			texts, media = nil, false
		}
		texts = append(texts, text)
		media = media || hasMedia
	}
	return boundedInput(strings.Join(texts, "\n"), media)
}

func userText(raw json.RawMessage, gemini bool) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, false
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return "", false
	}
	texts := []string{}
	media := false
	for _, part := range parts {
		var kind, content string
		_ = json.Unmarshal(part["type"], &kind)
		if kind == "text" || kind == "input_text" || gemini && kind == "" && part["text"] != nil {
			if json.Unmarshal(part["text"], &content) == nil {
				texts = append(texts, content)
			}
		} else if kind != "tool_result" && kind != "tool_use" && part["functionCall"] == nil && part["functionResponse"] == nil {
			media = true
		}
	}
	return strings.Join(texts, "\n"), media
}

func boundedInput(text string, media bool) Input {
	if utf8.RuneCountInString(text) > MaxTextRunes {
		return Input{Media: media, Reason: "text_too_large"}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		reason := "no_user_text"
		if media {
			reason = "media_only"
		}
		return Input{Media: media, Reason: reason}
	}
	return Input{Text: text, Media: media}
}
