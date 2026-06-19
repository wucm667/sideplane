package sidecar

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckReadablePathsReportsReadableAndMissing(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"model":"test"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	missing := filepath.Join(dir, "missing.json")

	statuses := CheckReadablePaths([]string{configPath, missing, " "})
	if len(statuses) != 2 {
		t.Fatalf("statuses = %#v, want 2 entries", statuses)
	}
	if statuses[0].Path != configPath || !statuses[0].Readable || statuses[0].Error != "" {
		t.Fatalf("readable status = %#v, want readable config", statuses[0])
	}
	if statuses[1].Path != missing || statuses[1].Readable || !strings.Contains(statuses[1].Error, "stat") {
		t.Fatalf("missing status = %#v, want stat error", statuses[1])
	}
}

func TestCheckReadablePathsRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	statuses := CheckReadablePaths([]string{dir})
	if len(statuses) != 1 {
		t.Fatalf("statuses = %#v, want one entry", statuses)
	}
	if statuses[0].Readable || statuses[0].Error != "not a regular file" {
		t.Fatalf("directory status = %#v, want not regular", statuses[0])
	}
}
