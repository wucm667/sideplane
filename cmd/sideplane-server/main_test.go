package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsInvalidFreshnessDurations(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "offline before stale",
			args: []string{
				"--stale-after=10m",
				"--offline-after=2m",
			},
		},
		{
			name: "offline equals stale",
			args: []string{
				"--stale-after=10m",
				"--offline-after=10m",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			code := run(tt.args, &stdout, &stderr)

			if code == 0 {
				t.Fatalf("exit code = 0, want non-zero")
			}
			if !strings.Contains(stderr.String(), "offline-after must be greater than stale-after") {
				t.Fatalf("stderr = %q, want freshness validation error", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestRunCreatesSigningKeyFromFlagBeforeListenFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on temp port: %v", err)
	}
	defer listener.Close()
	keyPath := filepath.Join(t.TempDir(), "signing-key.json")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"--addr", listener.Addr().String(),
		"--db", filepath.Join(t.TempDir(), "sideplane.db"),
		"--signing-key", keyPath,
	}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("exit code = 0, want listen failure")
	}
	if !strings.Contains(stderr.String(), "address already in use") {
		t.Fatalf("stderr = %q, want listen failure", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat signing key: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("signing key mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestRunWarnsWhenSigningKeyIsEphemeral(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on temp port: %v", err)
	}
	defer listener.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"--addr", listener.Addr().String(),
		"--db", filepath.Join(t.TempDir(), "sideplane.db"),
		"--operator-token", "dev-token",
	}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("exit code = 0, want listen failure")
	}
	if !strings.Contains(stderr.String(), "ephemeral in-memory key") {
		t.Fatalf("stderr = %q, want ephemeral signing key warning", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}
