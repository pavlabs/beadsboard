package agent

import (
	"encoding/json"
	"strings"
)

// decode parses one stream-json line into a generic object. Non-JSON lines
// (e.g. stray stderr interleaving) are ignored.
func decode(line []byte) (map[string]any, bool) {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 || line[0] != '{' {
		return nil, false
	}
	var ev map[string]any
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, false
	}
	return ev, true
}

func sessionID(ev map[string]any) string {
	s, _ := ev["session_id"].(string)
	return s
}

// assistantText renders an assistant event into a one-line progress note: its
// text and any tool it invoked. It tolerates both the nested message shape and a
// flat text field.
func assistantText(ev map[string]any) string {
	if msg, ok := ev["message"].(map[string]any); ok {
		if content, ok := msg["content"].([]any); ok {
			var parts []string
			for _, c := range content {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				switch cm["type"] {
				case "text":
					if t, _ := cm["text"].(string); strings.TrimSpace(t) != "" {
						parts = append(parts, firstLine(t))
					}
				case "tool_use":
					if n, _ := cm["name"].(string); n != "" {
						parts = append(parts, "→ "+n)
					}
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "  ")
			}
		}
	}
	if t, _ := ev["text"].(string); strings.TrimSpace(t) != "" {
		return firstLine(t)
	}
	return ""
}

func resultText(ev map[string]any) string {
	r, _ := ev["result"].(string)
	return r
}
