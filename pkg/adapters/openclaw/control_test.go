package openclaw

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wucm667/sideplane/pkg/adapters"
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
	adapter := NewAdapter(WithServiceUnit("openclaw.service"), WithAllowLiveApply(true))
	adapter.runCommand = runner.run

	if err := adapter.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}
