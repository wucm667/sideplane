package openclaw

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/protocol"
)

// WithAllowLiveApply permits the adapter to restart the managed runtime.
// When false (the default), Restart and HealthCheck return
// adapters.ErrLiveApplyDisabled.
func WithAllowLiveApply(allow bool) Option {
	return func(a *Adapter) {
		a.allowLive = allow
	}
}

// WithServiceUnit configures an allowlisted systemd unit restart target.
func WithServiceUnit(unit string) Option {
	return func(a *Adapter) {
		a.serviceUnitName = strings.TrimSpace(unit)
	}
}

// WithRestartSudo prefixes systemd restart with sudo -n when enabled.
// It applies only to the allowlisted systemctl restart path.
func WithRestartSudo(useSudo bool) Option {
	return func(a *Adapter) {
		a.restartSudo = useSudo
	}
}

// Restart restarts the managed OpenClaw runtime using an allowlisted operation:
// a Docker container restart when a container is configured, otherwise a
// systemd unit restart. It never offers general command execution.
func (a *Adapter) Restart(ctx context.Context) error {
	if !a.allowLive {
		return adapters.ErrLiveApplyDisabled
	}
	if container := a.dockerContainer(); container != "" {
		if _, err := a.runDocker(ctx, "restart", container); err != nil {
			return fmt.Errorf("restart openclaw container: %w", err)
		}
		return nil
	}
	if unit := a.serviceUnit(); unit != "" {
		if _, err := a.runSystemdRestart(ctx, unit); err != nil {
			return fmt.Errorf("restart openclaw service %s: %w", unit, err)
		}
		return nil
	}
	return errors.New("no allowlisted openclaw restart target configured (set a docker container or service unit)")
}

// HealthCheck reports whether OpenClaw is running after a change. It is
// read-only and uses allowlisted inspection only.
func (a *Adapter) HealthCheck(ctx context.Context) error {
	if !a.allowLive {
		return adapters.ErrLiveApplyDisabled
	}
	if container := a.dockerContainer(); container != "" {
		out, err := a.runDocker(ctx, "inspect", "--format", "{{.State.Running}}", container)
		if err != nil {
			return fmt.Errorf("inspect openclaw container: %w", err)
		}
		if strings.TrimSpace(string(out)) != "true" {
			return fmt.Errorf("openclaw container %s is not running after restart", container)
		}
		return nil
	}
	if unit := a.serviceUnit(); unit != "" {
		out, _ := a.runControl(ctx, "systemctl", "is-active", unit)
		if strings.TrimSpace(string(out)) != "active" {
			return fmt.Errorf("openclaw service %s is not active after restart", unit)
		}
		return nil
	}
	return errors.New("no allowlisted openclaw health target configured (set a docker container or service unit)")
}

// RuntimeHealth reports OpenClaw local liveness using read-only checks only.
func (a *Adapter) RuntimeHealth(ctx context.Context) (protocol.RuntimeHealth, error) {
	reasons := []string{}
	path, err := a.findConfigPath()
	if err != nil {
		return protocol.RuntimeHealth{}, err
	}
	if path != "" {
		contents, err := os.ReadFile(path)
		if err != nil {
			return adapters.RuntimeHealthDegraded("read openclaw config: " + err.Error()), nil
		}
		if err := validateConfigSyntax(path, contents); err != nil {
			return adapters.RuntimeHealthDegraded(err.Error()), nil
		}
		reasons = append(reasons, "config readable")
	} else {
		reasons = append(reasons, "config path not found")
	}
	if container := a.dockerContainer(); container != "" {
		out, err := a.runDocker(ctx, "inspect", "--format", "{{.State.Running}}", container)
		if err != nil {
			return adapters.RuntimeHealthDegraded("inspect openclaw container: " + err.Error()), nil
		}
		if strings.TrimSpace(string(out)) != "true" {
			return adapters.RuntimeHealthDegraded("openclaw container " + container + " is not running"), nil
		}
		reasons = append(reasons, "container running")
		return adapters.RuntimeHealthHealthy(strings.Join(reasons, "; ")), nil
	}
	if unit := a.serviceUnit(); unit != "" {
		out, _ := a.runControl(ctx, "systemctl", "is-active", unit)
		if strings.TrimSpace(string(out)) != "active" {
			return adapters.RuntimeHealthDegraded("openclaw service " + unit + " is not active"), nil
		}
		reasons = append(reasons, "service active")
		return adapters.RuntimeHealthHealthy(strings.Join(reasons, "; ")), nil
	}
	return adapters.RuntimeHealthUnknown(strings.Join(append(reasons, "no service or container target configured"), "; ")), nil
}

func (a *Adapter) serviceUnit() string {
	if a.serviceUnitName != "" {
		return a.serviceUnitName
	}
	getenv := a.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	return strings.TrimSpace(getenv("SIDEPLANE_OPENCLAW_SERVICE_UNIT"))
}

func (a *Adapter) runDocker(ctx context.Context, args ...string) ([]byte, error) {
	runner := a.runCommand
	if runner == nil {
		runner = runCommand
	}
	return runner(ctx, "docker", args...)
}

func (a *Adapter) runControl(ctx context.Context, name string, args ...string) ([]byte, error) {
	runner := a.runCommand
	if runner == nil {
		runner = runCommand
	}
	return runner(ctx, name, args...)
}

func (a *Adapter) runSystemdRestart(ctx context.Context, unit string) ([]byte, error) {
	if a.restartSudo {
		return a.runControl(ctx, "sudo", "-n", "systemctl", "restart", unit)
	}
	return a.runControl(ctx, "systemctl", "restart", unit)
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}
