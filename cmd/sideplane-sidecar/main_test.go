package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wucm667/sideplane/internal/sidecar"
)

func TestResolveRuntimeConfigLoadsStateAndAppliesOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := sidecar.WriteState(path, sidecar.SidecarState{
		ServerURL:      "http://state-server:8080",
		NodeID:         "state-node",
		NodeCredential: "state-credential",
		EnrolledAt:     time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("write state: %v", err)
	}

	cfg, err := resolveRuntimeConfig("http://flag-server:8080", "flag-node", "flag-credential", path)
	if err != nil {
		t.Fatalf("resolve runtime config: %v", err)
	}
	if cfg.ServerURL != "http://flag-server:8080" {
		t.Fatalf("server URL = %q, want flag override", cfg.ServerURL)
	}
	if cfg.NodeID != "flag-node" {
		t.Fatalf("node ID = %q, want flag override", cfg.NodeID)
	}
	if cfg.NodeCredential != "state-credential" {
		t.Fatalf("node credential = %q, want state credential", cfg.NodeCredential)
	}
	if cfg.StatePath != path {
		t.Fatalf("state path = %q, want %q", cfg.StatePath, path)
	}
}

func TestResolveRuntimeConfigUsesCredentialFlagWhenStateMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-state.json")

	cfg, err := resolveRuntimeConfig("http://localhost:8080", "node-flag", "flag-credential", path)
	if err != nil {
		t.Fatalf("resolve runtime config: %v", err)
	}
	if cfg.ServerURL != "http://localhost:8080" {
		t.Fatalf("server URL = %q, want flag server", cfg.ServerURL)
	}
	if cfg.NodeID != "node-flag" {
		t.Fatalf("node ID = %q, want flag node", cfg.NodeID)
	}
	if cfg.NodeCredential != "flag-credential" {
		t.Fatalf("node credential = %q, want flag credential", cfg.NodeCredential)
	}
}

func TestRunRequiresNodeCredential(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "missing-state.json")

	code := run([]string{
		"--server", "http://localhost:8080",
		"--state", path,
	}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr.String(), "run sideplane-sidecar enroll first") {
		t.Fatalf("stderr = %q, want enroll hint", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestSplitPathListAcceptsPathListCommasAndNewlines(t *testing.T) {
	raw := " /etc/hermes/config.json " + string(os.PathListSeparator) + " /opt/hermes/config.yaml,/tmp/hermes.env\n"
	paths := splitPathList(raw)
	want := []string{"/etc/hermes/config.json", "/opt/hermes/config.yaml", "/tmp/hermes.env"}
	if len(paths) != len(want) {
		t.Fatalf("len(paths) = %d, want %d: %#v", len(paths), len(want), paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}
