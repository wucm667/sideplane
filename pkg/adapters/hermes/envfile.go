package hermes

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	managedEnvStartMarker = "# >>> sideplane-managed >>>"
	managedEnvEndMarker   = "# <<< sideplane-managed <<<"
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// RenderManagedEnv returns a .env file with Sideplane's managed provider
// secrets block appended or replaced. Bytes outside the managed block are
// preserved exactly.
func RenderManagedEnv(current []byte, secrets map[string]string) ([]byte, error) {
	names := make([]string, 0, len(secrets))
	for name := range secrets {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !envNamePattern.MatchString(name) {
			return nil, fmt.Errorf("managed env name %q is invalid", name)
		}
		names = append(names, name)
	}
	slices.Sort(names)

	block := renderManagedEnvBlock(names, secrets)
	start, end, found, err := managedEnvBlockRange(current)
	if err != nil {
		return nil, err
	}
	if found {
		out := make([]byte, 0, len(current)-(end-start)+len(block))
		out = append(out, current[:start]...)
		out = append(out, block...)
		out = append(out, current[end:]...)
		return out, nil
	}

	out := make([]byte, 0, len(current)+len(block)+1)
	out = append(out, current...)
	if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, '\n')
	}
	out = append(out, block...)
	return out, nil
}

func renderManagedEnvBlock(names []string, secrets map[string]string) []byte {
	var b strings.Builder
	b.WriteString(managedEnvStartMarker)
	b.WriteByte('\n')
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(quoteEnvValue(secrets[name]))
		b.WriteByte('\n')
	}
	b.WriteString(managedEnvEndMarker)
	b.WriteByte('\n')
	return []byte(b.String())
}

func managedEnvBlockRange(contents []byte) (start int, end int, found bool, err error) {
	startIndex := bytes.Index(contents, []byte(managedEnvStartMarker))
	endIndex := bytes.Index(contents, []byte(managedEnvEndMarker))
	switch {
	case startIndex < 0 && endIndex < 0:
		return 0, 0, false, nil
	case startIndex < 0 || endIndex < 0 || endIndex < startIndex:
		return 0, 0, false, fmt.Errorf("managed env block markers are unbalanced")
	}
	start = lineStart(contents, startIndex)
	end = lineEnd(contents, endIndex+len(managedEnvEndMarker))
	return start, end, true, nil
}

func lineStart(contents []byte, index int) int {
	for index > 0 && contents[index-1] != '\n' {
		index--
	}
	return index
}

func lineEnd(contents []byte, index int) int {
	for index < len(contents) && contents[index] != '\n' {
		index++
	}
	if index < len(contents) && contents[index] == '\n' {
		index++
	}
	return index
}

func quoteEnvValue(value string) string {
	if value == "" {
		return `""`
	}
	if envValueCanBeUnquoted(value) {
		return value
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func envValueCanBeUnquoted(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '@' || r == '%' || r == '+' || r == '=' || r == ',':
		default:
			return false
		}
	}
	return true
}
