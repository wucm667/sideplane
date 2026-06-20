package hermes

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
	for _, c := range r.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

// assertVerifies that the adapter implements the optional ServiceController.
var _ adapters.ServiceController = (*Adapter)(nil)
var _ adapters.HealthChecker = (*Adapter)(nil)

func TestRestartDisabledIsNoOp(t *testing.T) {
	runner := &recordingRunner{}
	a := NewAdapter(WithDockerContainer("hermes"), WithAllowLiveApply(false))
	a.runCommand = runner.run

	if err := a.Restart(context.Background()); !errors.Is(err, adapters.ErrLiveApplyDisabled) {
		t.Fatalf("Restart err = %v, want ErrLiveApplyDisabled", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner invoked while disabled: %v", runner.joined())
	}
}

func TestRestartDockerContainer(t *testing.T) {
	runner := &recordingRunner{}
	a := NewAdapter(WithDockerContainer("hermes"), WithAllowLiveApply(true))
	a.runCommand = runner.run

	if err := a.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if got := runner.joined(); len(got) != 1 || got[0] != "docker restart hermes" {
		t.Errorf("calls = %v, want [docker restart hermes]", got)
	}
}

func TestRestartPrefersDockerContainerOverSystemdUnit(t *testing.T) {
	runner := &recordingRunner{}
	a := NewAdapter(WithDockerContainer("hermes"), WithServiceUnit("hermes.service"), WithAllowLiveApply(true))
	a.runCommand = runner.run

	if err := a.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if got := runner.joined(); len(got) != 1 || got[0] != "docker restart hermes" {
		t.Errorf("calls = %v, want [docker restart hermes]", got)
	}
}

func TestRestartDockerError(t *testing.T) {
	runner := &recordingRunner{fn: func(string, []string) ([]byte, error) {
		return []byte("boom"), errors.New("exit 1")
	}}
	a := NewAdapter(WithDockerContainer("hermes"), WithAllowLiveApply(true))
	a.runCommand = runner.run

	if err := a.Restart(context.Background()); err == nil {
		t.Fatal("expected restart error, got nil")
	}
}

func TestRestartSystemdUnit(t *testing.T) {
	runner := &recordingRunner{}
	a := NewAdapter(WithServiceUnit("hermes.service"), WithAllowLiveApply(true))
	a.runCommand = runner.run

	if err := a.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if got := runner.joined(); len(got) != 1 || got[0] != "systemctl restart hermes.service" {
		t.Errorf("calls = %v, want [systemctl restart hermes.service]", got)
	}
}

func TestRestartNoTarget(t *testing.T) {
	a := NewAdapter(WithAllowLiveApply(true))
	a.runCommand = (&recordingRunner{}).run
	a.getenv = func(string) string { return "" }

	if err := a.Restart(context.Background()); err == nil {
		t.Fatal("expected no-target error, got nil")
	}
}

func TestHealthCheckDisabledIsNoOp(t *testing.T) {
	a := NewAdapter(WithDockerContainer("hermes"), WithAllowLiveApply(false))
	if err := a.HealthCheck(context.Background()); !errors.Is(err, adapters.ErrLiveApplyDisabled) {
		t.Fatalf("HealthCheck err = %v, want ErrLiveApplyDisabled", err)
	}
}

func TestHealthCheckDockerRunning(t *testing.T) {
	runner := &recordingRunner{fn: func(_ string, args []string) ([]byte, error) {
		if strings.Join(args, " ") == "inspect --format {{.State.Running}} hermes" {
			return []byte("true\n"), nil
		}
		return nil, errors.New("unexpected call")
	}}
	a := NewAdapter(WithDockerContainer("hermes"), WithAllowLiveApply(true))
	a.runCommand = runner.run

	if err := a.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

func TestHealthCheckDockerNotRunning(t *testing.T) {
	runner := &recordingRunner{fn: func(string, []string) ([]byte, error) {
		return []byte("false\n"), nil
	}}
	a := NewAdapter(WithDockerContainer("hermes"), WithAllowLiveApply(true))
	a.runCommand = runner.run

	if err := a.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected unhealthy error for stopped container, got nil")
	}
}

func TestHealthCheckSystemdActive(t *testing.T) {
	runner := &recordingRunner{fn: func(_ string, args []string) ([]byte, error) {
		if strings.Join(args, " ") == "is-active hermes.service" {
			return []byte("active\n"), nil
		}
		return nil, errors.New("unexpected call")
	}}
	a := NewAdapter(WithServiceUnit("hermes.service"), WithAllowLiveApply(true))
	a.runCommand = runner.run

	if err := a.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

func TestHealthCheckSystemdInactive(t *testing.T) {
	runner := &recordingRunner{fn: func(string, []string) ([]byte, error) {
		return []byte("inactive\n"), errors.New("exit 3")
	}}
	a := NewAdapter(WithServiceUnit("hermes.service"), WithAllowLiveApply(true))
	a.runCommand = runner.run

	if err := a.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected inactive error, got nil")
	}
}

func TestRuntimeHealthDockerRunningReadOnly(t *testing.T) {
	runner := &recordingRunner{fn: func(_ string, args []string) ([]byte, error) {
		if strings.Join(args, " ") == "inspect --format {{.State.Running}} hermes" {
			return []byte("true\n"), nil
		}
		return nil, errors.New("unexpected call")
	}}
	a := NewAdapter(WithDockerContainer("hermes"), WithAllowLiveApply(false))
	a.runCommand = runner.run
	a.getenv = func(string) string { return "" }

	health, err := a.RuntimeHealth(context.Background())
	if err != nil {
		t.Fatalf("RuntimeHealth error = %v", err)
	}
	if health.State != protocol.RuntimeHealthHealthy || !strings.Contains(health.Reason, "container running") {
		t.Fatalf("health = %#v, want healthy container running", health)
	}
	if got := runner.joined(); len(got) != 1 || got[0] != "docker inspect --format {{.State.Running}} hermes" {
		t.Fatalf("calls = %v, want read-only docker inspect", got)
	}
}

func TestRuntimeHealthMalformedConfigDegraded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hermes.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	a := NewAdapter(WithConfigPaths(path))
	a.getenv = func(string) string { return "" }

	health, err := a.RuntimeHealth(context.Background())
	if err != nil {
		t.Fatalf("RuntimeHealth error = %v", err)
	}
	if health.State != protocol.RuntimeHealthDegraded || !strings.Contains(health.Reason, "parse hermes JSON config") {
		t.Fatalf("health = %#v, want degraded malformed config", health)
	}
}

func TestRuntimeHealthNoTargetUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hermes.json")
	if err := os.WriteFile(path, []byte(`{"model":{"provider":"openai","default":"gpt-5"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	a := NewAdapter(WithConfigPaths(path))
	a.getenv = func(string) string { return "" }

	health, err := a.RuntimeHealth(context.Background())
	if err != nil {
		t.Fatalf("RuntimeHealth error = %v", err)
	}
	if health.State != protocol.RuntimeHealthUnknown || !strings.Contains(health.Reason, "no service or container target configured") {
		t.Fatalf("health = %#v, want unknown without target", health)
	}
}
