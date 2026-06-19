package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestServerEnvFallbacksApplyWhenFlagsUnset(t *testing.T) {
	t.Setenv("SIDEPLANE_ADDR", "127.0.0.1:18080")
	t.Setenv("SIDEPLANE_DB_PATH", "/var/lib/sideplane/env.db")
	t.Setenv("SIDEPLANE_WEB_DIR", "/usr/share/sideplane/web")
	t.Setenv("SIDEPLANE_STALE_AFTER", "90s")
	t.Setenv("SIDEPLANE_OFFLINE_AFTER", "6m")
	t.Setenv("SIDEPLANE_HEARTBEAT_RETENTION", "250")
	t.Setenv("SIDEPLANE_JOB_RETENTION", "720h")
	t.Setenv("SIDEPLANE_AUDIT_RETENTION", "4320h")

	addr := ":8080"
	dbPath := "sideplane.db"
	webDir := ""
	staleAfter := 2 * time.Minute
	offlineAfter := 10 * time.Minute
	heartbeatRetention := 100
	jobRetention := 30 * 24 * time.Hour
	auditRetention := 180 * 24 * time.Hour

	if err := applyServerEnvFallbacks(map[string]bool{}, serverFlagValues{
		addr:               &addr,
		dbPath:             &dbPath,
		webDir:             &webDir,
		staleAfter:         &staleAfter,
		offlineAfter:       &offlineAfter,
		heartbeatRetention: &heartbeatRetention,
		jobRetention:       &jobRetention,
		auditRetention:     &auditRetention,
	}); err != nil {
		t.Fatalf("apply env fallbacks: %v", err)
	}

	if addr != "127.0.0.1:18080" {
		t.Fatalf("addr = %q, want env addr", addr)
	}
	if dbPath != "/var/lib/sideplane/env.db" {
		t.Fatalf("db path = %q, want env db", dbPath)
	}
	if webDir != "/usr/share/sideplane/web" {
		t.Fatalf("web dir = %q, want env web dir", webDir)
	}
	if staleAfter != 90*time.Second {
		t.Fatalf("stale after = %s, want 90s", staleAfter)
	}
	if offlineAfter != 6*time.Minute {
		t.Fatalf("offline after = %s, want 6m", offlineAfter)
	}
	if heartbeatRetention != 250 {
		t.Fatalf("heartbeat retention = %d, want 250", heartbeatRetention)
	}
	if jobRetention != 720*time.Hour {
		t.Fatalf("job retention = %s, want 720h", jobRetention)
	}
	if auditRetention != 4320*time.Hour {
		t.Fatalf("audit retention = %s, want 4320h", auditRetention)
	}
}

func TestRunRejectsInvalidHeartbeatRetention(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--heartbeat-retention", "0"}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr.String(), "heartbeat-retention must be positive") {
		t.Fatalf("stderr = %q, want heartbeat retention validation error", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunRejectsNegativeRetention(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "job retention",
			args: []string{"--job-retention=-1s"},
			want: "job-retention must be zero or positive",
		},
		{
			name: "audit retention",
			args: []string{"--audit-retention=-1s"},
			want: "audit-retention must be zero or positive",
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
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.want)
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
