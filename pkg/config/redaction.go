package config

import (
	"encoding/json"
	"regexp"
	"strings"
)

const RedactedValue = "[REDACTED]"

var secretAssignmentPattern = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(token|credential|secret|password|api[_-]?key)|authorization)\b\s*[:=]\s*[^,\s]+`)

// RedactSecrets returns a copy of value with secret-bearing JSON object values redacted.
func RedactSecrets(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if IsSecretKey(key) {
				out[key] = RedactedValue
				continue
			}
			out[key] = RedactSecrets(nested)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, nested := range typed {
			out[i] = RedactSecrets(nested)
		}
		return out
	default:
		return value
	}
}

// RedactString redacts JSON object values and key=value style secret fragments.
func RedactString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}

	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		switch decoded.(type) {
		case map[string]any, []any:
			encoded, err := json.Marshal(RedactSecrets(decoded))
			if err == nil {
				return string(encoded)
			}
		}
	}

	return secretAssignmentPattern.ReplaceAllStringFunc(raw, func(match string) string {
		index := strings.IndexAny(match, ":=")
		if index < 0 {
			return RedactedValue
		}
		return strings.TrimSpace(match[:index+1]) + RedactedValue
	})
}

// IsSecretKey reports whether a structured key conventionally carries secret material.
func IsSecretKey(key string) bool {
	normalized := normalizeSecretKey(key)
	switch {
	case normalized == "authorization":
		return true
	case normalized == "apikey" || strings.HasSuffix(normalized, "apikey"):
		return true
	case strings.HasSuffix(normalized, "token"):
		return true
	case strings.HasSuffix(normalized, "credential"):
		return true
	case strings.HasSuffix(normalized, "secret"):
		return true
	case strings.HasSuffix(normalized, "password"):
		return true
	default:
		return false
	}
}

func normalizeSecretKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	replacer := strings.NewReplacer("_", "", "-", "", ".", "", " ", "")
	return replacer.Replace(key)
}
