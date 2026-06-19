package buildinfo

import "strings"

// These variables are set by release builds through -ldflags -X.
var (
	Version   = "dev"
	Commit    = ""
	BuildDate = ""
)

// Format returns a compact human-readable binary version line.
func Format(binaryName string) string {
	name := strings.TrimSpace(binaryName)
	if name == "" {
		name = "sideplane"
	}

	version := strings.TrimSpace(Version)
	if version == "" {
		version = "dev"
	}

	details := []string{}
	if commit := strings.TrimSpace(Commit); commit != "" {
		details = append(details, "commit "+commit)
	}
	if buildDate := strings.TrimSpace(BuildDate); buildDate != "" {
		details = append(details, "built "+buildDate)
	}
	if len(details) == 0 {
		return name + " " + version
	}
	return name + " " + version + " (" + strings.Join(details, ", ") + ")"
}

// Labels returns normalized values for Prometheus build-info labels.
func Labels() (version string, commit string, buildDate string) {
	version = strings.TrimSpace(Version)
	if version == "" {
		version = "dev"
	}
	return version, strings.TrimSpace(Commit), strings.TrimSpace(BuildDate)
}
