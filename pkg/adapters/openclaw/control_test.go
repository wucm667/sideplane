package openclaw

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/protocol"
)

type recordingRunner struct {
	calls [][]string
	fn    func(name string, args []string) ([]byte, error)
}

func (r *recordingRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if r.fn != nil {
		return r.fn(name, args)
	}
	return nil, nil
}

func (r *recordingRunner) joined() []string {
	out := make([]string, 0, len(r.calls))
	for _, call := range r.calls {
		out = append(out, strings.Join(call, " "))
	}
	return out
}

var _ adapters.ServiceController = (*Adapter)(nil)
var _ adapters.HealthChecker = (*Adapter)(nil)

func TestRestartDisabledIsNoOp(t *testing.T) {
	runner := &recordingRunner{}
	adapter := NewAdapter(WithDockerContainer("openclaw"), WithAllowLiveApply(false))
	adapter.runCommand = runner.run

	if err := adapter.Restart(context.Background()); !errors.Is(err, adapters.ErrLiveApplyDisabled) {
		t.Fatalf("Restart err = %v, want ErrLiveApplyDisabled", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner invoked while disabled: %v", runner.joined())
	}
}

func TestRestartDockerContainer(t *testing.T) {
	runner := &recordingRunner{}
	adapter := NewAdapter(WithDockerContainer("openclaw"), WithAllowLiveApply(true))
	adapter.runCommand = runner.run

	if err := adapter.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if got := runner.joined(); len(got) != 1 || got[0] != "docker restart openclaw" {
		t.Fatalf("calls = %v, want [docker restart openclaw]", got)
	}
}

func TestRestartSystemdUnit(t *testing.T) {
	runner := &recordingRunner{}
	adapter := NewAdapter(WithServiceUnit("openclaw.service"), WithAllowLiveApply(true))
	adapter.runCommand = runner.run

	if err := adapter.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if got := runner.joined(); len(got) != 1 || got[0] != "systemctl restart openclaw.service" {
		t.Fatalf("calls = %v, want [systemctl restart openclaw.service]", got)
	}
}

func TestRestartSystemdUnitWithSudo(t *testing.T) {
	runner := &recordingRunner{}
	adapter := NewAdapter(WithServiceUnit("openclaw.service"), WithAllowLiveApply(true), WithRestartSudo(true))
	adapter.runCommand = runner.run

	if err := adapter.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if got := runner.joined(); len(got) != 1 || got[0] != "sudo -n systemctl restart openclaw.service" {
		t.Fatalf("calls = %v, want [sudo -n systemctl restart openclaw.service]", got)
	}
}

func TestRestartPrefersDockerContainerOverSystemdUnit(t *testing.T) {
	runner := &recordingRunner{}
	adapter := NewAdapter(WithDockerContainer("openclaw"), WithServiceUnit("openclaw.service"), WithAllowLiveApply(true))
	adapter.runCommand = runner.run

	if err := adapter.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if got := runner.joined(); len(got) != 1 || got[0] != "docker restart openclaw" {
		t.Fatalf("calls = %v, want [docker restart openclaw]", got)
	}
}

func TestHealthCheckDockerNotRunning(t *testing.T) {
	runner := &recordingRunner{fn: func(string, []string) ([]byte, error) {
		return []byte("false\n"), nil
	}}
	adapter := NewAdapter(WithDockerContainer("openclaw"), WithAllowLiveApply(true))
	adapter.runCommand = runner.run

	if err := adapter.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected unhealthy error for stopped container, got nil")
	}
}

func TestHealthCheckSystemdActive(t *testing.T) {
	runner := &recordingRunner{fn: func(_ string, args []string) ([]byte, error) {
		if strings.Join(args, " ") == "is-active openclaw.service" {
			return []byte("active\n"), nil
		}
		return nil, errors.New("unexpected call")
	}}
	adapter := NewAdapter(WithServiceUnit("openclaw.service"), WithAllowLiveApply(true), WithRestartSudo(true))
	adapter.runCommand = runner.run

	if err := adapter.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if got := runner.joined(); len(got) != 1 || got[0] != "systemctl is-active openclaw.service" {
		t.Fatalf("calls = %v, want read-only systemctl is-active without sudo", got)
	}
}

func TestRuntimeHealthDockerRunningReadOnly(t *testing.T) {
	runner := &recordingRunner{fn: func(_ string, args []string) ([]byte, error) {
		if strings.Join(args, " ") == "inspect --format {{.State.Running}} openclaw" {
			return []byte("true\n"), nil
		}
		return nil, errors.New("unexpected call")
	}}
	adapter := NewAdapter(WithDockerContainer("openclaw"), WithAllowLiveApply(false))
	adapter.runCommand = runner.run
	adapter.getenv = func(string) string { return "" }
	adapter.defaultConfigPaths = []string{}

	health, err := adapter.RuntimeHealth(context.Background())
	if err != nil {
		t.Fatalf("RuntimeHealth error = %v", err)
	}
	if health.State != protocol.RuntimeHealthHealthy || !strings.Contains(health.Reason, "container running") {
		t.Fatalf("health = %#v, want healthy container running", health)
	}
	if got := runner.joined(); len(got) != 1 || got[0] != "docker inspect --format {{.State.Running}} openclaw" {
		t.Fatalf("calls = %v, want read-only docker inspect", got)
	}
}

func TestRuntimeHealthMalformedConfigDegraded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openclaw.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	adapter := NewAdapter(WithConfigPaths(path))
	adapter.getenv = func(string) string { return "" }
	adapter.defaultConfigPaths = []string{}

	health, err := adapter.RuntimeHealth(context.Background())
	if err != nil {
		t.Fatalf("RuntimeHealth error = %v", err)
	}
	if health.State != protocol.RuntimeHealthDegraded || !strings.Contains(health.Reason, "parse openclaw JSON config") {
		t.Fatalf("health = %#v, want degraded malformed config", health)
	}
}

func TestRuntimeHealthNoTargetUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openclaw.json")
	if err := os.WriteFile(path, []byte(`{"provider":"openai","model":"gpt-5"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	adapter := NewAdapter(WithConfigPaths(path))
	adapter.getenv = func(string) string { return "" }
	adapter.defaultConfigPaths = []string{}

	health, err := adapter.RuntimeHealth(context.Background())
	if err != nil {
		t.Fatalf("RuntimeHealth error = %v", err)
	}
	if health.State != protocol.RuntimeHealthUnknown || !strings.Contains(health.Reason, "no service or container target configured") {
		t.Fatalf("health = %#v, want unknown without target", health)
	}
}
