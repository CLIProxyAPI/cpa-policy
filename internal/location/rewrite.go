package location

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Rewrite changes only recognized structured contexts and web-search locations.
// It preserves opaque payloads, absolute timestamps, and ordinary chat text.
func Rewrite(body []byte, configured *Compiled, now time.Time) ([]byte, error) {
	if configured == nil {
		return body, nil
	}
	if !json.Valid(body) || !gjson.ParseBytes(body).IsObject() {
		return body, nil
	}
	date := configured.Date(now)
	result := body
	set := func(path string, value any) error {
		updated, err := sjson.SetBytes(result, path, value)
		if err != nil {
			return fmt.Errorf("rewrite structured location field: %w", err)
		}
		result = updated
		return nil
	}
	input := gjson.GetBytes(body, "input")
	if input.IsArray() {
		for i, item := range input.Array() {
			if !item.IsObject() {
				continue
			}
			if kind := item.Get("type"); kind.Exists() && kind.String() != "message" {
				continue
			}
			var tag, kind string
			switch item.Get("role").String() {
			case "user":
				tag, kind = "environment_context", "environments.environment_context"
			case "developer":
				tag, kind = "codex_apps_client_time_context", "additional_content.codex_apps_client_time_context"
			default:
				continue
			}
			content := item.Get("content")
			if !content.IsArray() {
				continue
			}
			kinds := item.Get("internal_chat_message_metadata_passthrough.content_item_kinds")
			for j, part := range content.Array() {
				// Explicit classification is authoritative, including empty/invalid lists.
				if kinds.Exists() && (!kinds.IsArray() || kinds.Get(fmt.Sprint(j)).String() != kind) {
					continue
				}
				text := part.Get("text")
				if part.Get("type").String() != "input_text" || text.Type != gjson.String {
					continue
				}
				if aligned := alignContext(text.String(), tag, date, configured.Timezone); aligned != text.String() {
					if err := set(fmt.Sprintf("input.%d.content.%d.text", i, j), aligned); err != nil {
						return body, err
					}
				}
			}
		}
	}
	tools := gjson.GetBytes(body, "tools")
	if tools.IsArray() {
		for i, tool := range tools.Array() {
			kind := tool.Get("type").String()
			if !tool.IsObject() || (kind != "web_search" && !strings.HasPrefix(kind, "web_search_")) {
				continue
			}
			if err := set(fmt.Sprintf("tools.%d.user_location", i), map[string]string{
				"type": "approximate", "country": configured.Country, "region": configured.Region,
				"city": configured.City, "timezone": configured.Timezone,
			}); err != nil {
				return body, err
			}
		}
	}
	return result, nil
}

type replacement struct {
	start, end int
	text       string
}

// Parse the complete XML block before editing. Offset edits preserve unrelated
// whitespace, text, nested examples, attributes, and environment details.
func alignContext(text, tag, date, timezone string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "<"+tag+">") || !strings.HasSuffix(trimmed, "</"+tag+">") {
		return text
	}
	decoder := xml.NewDecoder(strings.NewReader(text))
	depth, roots := 0, 0
	var changes []replacement
	var candidate *replacement
	for {
		start := int(decoder.InputOffset())
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return text
		}
		switch token := token.(type) {
		case xml.Directive:
			return text
		case xml.StartElement:
			depth++
			if depth == 1 {
				roots++
				if roots != 1 || token.Name.Local != tag || token.Name.Space != "" {
					return text
				}
			}
			if depth == 2 && token.Name.Space == "" {
				var value string
				switch token.Name.Local {
				case "timezone":
					value = timezone
				case "current_date":
					value = date
				}
				if value != "" {
					candidate = &replacement{start: start, text: "<" + token.Name.Local + ">" + value + "</" + token.Name.Local + ">"}
				}
			}
		case xml.EndElement:
			if depth == 2 && candidate != nil {
				candidate.end = int(decoder.InputOffset())
				changes = append(changes, *candidate)
				candidate = nil
			}
			depth--
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(token)) != "" {
				return text
			}
		}
	}
	if depth != 0 || roots != 1 {
		return text
	}
	for i := len(changes) - 1; i >= 0; i-- {
		change := changes[i]
		text = text[:change.start] + change.text + text[change.end:]
	}
	return text
}
