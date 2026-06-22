package adapters

import (
	"errors"
	"path/filepath"
	"strings"
)

// ImageTagVersion returns the version-like tag portion of a Docker image
// reference. When no tag is present, it returns the trimmed reference itself so
// operators still see the exact image value Docker reported.
func ImageTagVersion(imageRef string) string {
	ref := strings.TrimSpace(imageRef)
	if ref == "" {
		return ""
	}
	beforeDigest := ref
	if idx := strings.Index(beforeDigest, "@"); idx >= 0 {
		beforeDigest = beforeDigest[:idx]
	}
	lastSlash := strings.LastIndex(beforeDigest, "/")
	lastColon := strings.LastIndex(beforeDigest, ":")
	if lastColon > lastSlash && lastColon < len(beforeDigest)-1 {
		return strings.TrimSpace(beforeDigest[lastColon+1:])
	}
	return ref
}

// ParseVersionCommand splits one operator-configured command into argv without
// invoking a shell. The configured command is the allowlist; callers must not
// append runtime-supplied arguments.
func ParseVersionCommand(command string) (string, []string, error) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return "", nil, nil
	}
	if isShellCommand(fields[0]) {
		return "", nil, errors.New("version command must not invoke a shell")
	}
	return fields[0], append([]string(nil), fields[1:]...), nil
}

func isShellCommand(name string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	base = strings.TrimSuffix(base, ".exe")
	switch base {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "csh", "tcsh", "pwsh", "powershell", "cmd":
		return true
	default:
		return false
	}
}
