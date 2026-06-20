package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wucm667/sideplane/internal/store"
	"github.com/wucm667/sideplane/pkg/protocol"
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
	t.Setenv("SIDEPLANE_ROLLOUT_INTERVAL", "15s")
	t.Setenv("SIDEPLANE_ENROLLMENT_RATE_LIMIT", "7")
	t.Setenv("SIDEPLANE_OPERATOR_AUTH_RATE_LIMIT", "11")
	t.Setenv("SIDEPLANE_RATE_LIMIT_WINDOW", "45s")

	addr := ":8080"
	dbPath := "sideplane.db"
	webDir := ""
	staleAfter := 2 * time.Minute
	offlineAfter := 10 * time.Minute
	heartbeatRetention := 100
	jobRetention := 30 * 24 * time.Hour
	auditRetention := 180 * 24 * time.Hour
	rolloutInterval := defaultRolloutInterval
	enrollmentRateLimit := 20
	operatorAuthRateLimit := 60
	rateLimitWindow := time.Minute

	if err := applyServerEnvFallbacks(map[string]bool{}, serverFlagValues{
		addr:                  &addr,
		dbPath:                &dbPath,
		webDir:                &webDir,
		staleAfter:            &staleAfter,
		offlineAfter:          &offlineAfter,
		heartbeatRetention:    &heartbeatRetention,
		jobRetention:          &jobRetention,
		auditRetention:        &auditRetention,
		rolloutInterval:       &rolloutInterval,
		enrollmentRateLimit:   &enrollmentRateLimit,
		operatorAuthRateLimit: &operatorAuthRateLimit,
		rateLimitWindow:       &rateLimitWindow,
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
	if rolloutInterval != 15*time.Second {
		t.Fatalf("rollout interval = %s, want 15s", rolloutInterval)
	}
	if enrollmentRateLimit != 7 {
		t.Fatalf("enrollment rate limit = %d, want 7", enrollmentRateLimit)
	}
	if operatorAuthRateLimit != 11 {
		t.Fatalf("operator auth rate limit = %d, want 11", operatorAuthRateLimit)
	}
	if rateLimitWindow != 45*time.Second {
		t.Fatalf("rate limit window = %s, want 45s", rateLimitWindow)
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

func TestRunRejectsNegativeRolloutInterval(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--rollout-interval=-1s"}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr.String(), "rollout-interval must be zero or positive") {
		t.Fatalf("stderr = %q, want rollout interval validation error", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunRejectsInvalidRateLimits(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "enrollment",
			args: []string{"--enrollment-rate-limit=-1"},
			want: "enrollment-rate-limit must be zero or positive",
		},
		{
			name: "operator auth",
			args: []string{"--operator-auth-rate-limit=-1"},
			want: "operator-auth-rate-limit must be zero or positive",
		},
		{
			name: "window",
			args: []string{"--rate-limit-window=0s"},
			want: "rate-limit-window must be positive",
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

func TestRunBackupSubcommandWritesUsableCopy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sideplane.db")
	src, err := store.OpenSQLiteNodeStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	if _, err := src.RecordHeartbeat(ctx, protocol.HeartbeatRequest{NodeID: "node-a", Hostname: "host-a"}, time.Now().UTC()); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("close source db: %v", err)
	}

	outPath := filepath.Join(dir, "snapshot.db")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"backup", "--db", dbPath, "--out", outPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("backup exit code = %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Backup written to "+outPath) {
		t.Fatalf("stdout = %q, want backup confirmation", stdout.String())
	}

	restored, err := store.OpenSQLiteNodeStore(ctx, outPath)
	if err != nil {
		t.Fatalf("open backup db: %v", err)
	}
	defer restored.Close()
	nodes, err := restored.ListNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes from backup: %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != "node-a" {
		t.Fatalf("backup nodes = %+v, want one node-a", nodes)
	}
}

func TestRunBackupSubcommandRequiresOut(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"backup", "--db", filepath.Join(t.TempDir(), "sideplane.db")}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--out is required") {
		t.Fatalf("stderr = %q, want --out is required", stderr.String())
	}
}

func TestPerformScheduledBackupWritesAndPrunes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src, err := store.OpenSQLiteNodeStore(ctx, filepath.Join(dir, "sideplane.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer src.Close()

	backupDir := filepath.Join(dir, "backups")
	const keep = 2
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	totalPruned := 0
	for i := 0; i < 5; i++ {
		_, pruned, err := performScheduledBackup(ctx, src, backupDir, keep, base.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("scheduled backup %d: %v", i, err)
		}
		totalPruned += pruned
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	backups := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), backupFilePrefix) && strings.HasSuffix(entry.Name(), ".db") {
			backups++
		}
	}
	if backups != keep {
		t.Fatalf("retained backups = %d, want %d", backups, keep)
	}
	if totalPruned != 3 {
		t.Fatalf("total pruned = %d, want 3", totalPruned)
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
