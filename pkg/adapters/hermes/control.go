package hermes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/wucm667/sideplane/pkg/adapters"
	"github.com/wucm667/sideplane/pkg/protocol"
)

// WithAllowLiveApply permits the adapter to restart the managed runtime.
// When false (the default), Restart and HealthCheck are no-ops that return
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

// Restart restarts the managed Hermes runtime using an allowlisted operation:
// a Docker container restart when a container is configured, otherwise a
// systemd unit restart. It never offers general command execution.
func (a *Adapter) Restart(ctx context.Context) error {
	if !a.allowLive {
		return adapters.ErrLiveApplyDisabled
	}
	if container := a.dockerContainer(); container != "" {
		if _, err := a.runDocker(ctx, "restart", container); err != nil {
			return fmt.Errorf("restart hermes container: %w", err)
		}
		return nil
	}
	if unit := a.serviceUnit(); unit != "" {
		if _, err := a.runSystemdRestart(ctx, unit); err != nil {
			return fmt.Errorf("restart hermes service %s: %w", unit, err)
		}
		return nil
	}
	return errors.New("no allowlisted hermes restart target configured (set a docker container or service unit)")
}

// HealthCheck reports whether Hermes is running after a change. It is read-only
// and uses allowlisted inspection only. Config validity is verified before the
// replace; this confirms the service came back up.
func (a *Adapter) HealthCheck(ctx context.Context) error {
	if !a.allowLive {
		return adapters.ErrLiveApplyDisabled
	}
	if container := a.dockerContainer(); container != "" {
		out, err := a.runDocker(ctx, "inspect", "--format", "{{.State.Running}}", container)
		if err != nil {
			return fmt.Errorf("inspect hermes container: %w", err)
		}
		if strings.TrimSpace(string(out)) != "true" {
			return fmt.Errorf("hermes container %s is not running after restart", container)
		}
		return nil
	}
	if unit := a.serviceUnit(); unit != "" {
		// systemctl is-active exits non-zero when inactive, so trust the output.
		out, _ := a.runControl(ctx, "systemctl", "is-active", unit)
		if strings.TrimSpace(string(out)) != "active" {
			return fmt.Errorf("hermes service %s is not active after restart", unit)
		}
		return nil
	}
	return errors.New("no allowlisted hermes health target configured (set a docker container or service unit)")
}

// RuntimeHealth reports Hermes local liveness using read-only checks only.
func (a *Adapter) RuntimeHealth(ctx context.Context) (protocol.RuntimeHealth, error) {
	reasons := []string{}
	path, err := a.findConfigPath()
	if err != nil {
		return protocol.RuntimeHealth{}, err
	}
	if path != "" {
		contents, err := os.ReadFile(path)
		if err != nil {
			return adapters.RuntimeHealthDegraded("read hermes config: " + err.Error()), nil
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
			return adapters.RuntimeHealthDegraded("inspect hermes container: " + err.Error()), nil
		}
		if strings.TrimSpace(string(out)) != "true" {
			return adapters.RuntimeHealthDegraded("hermes container " + container + " is not running"), nil
		}
		reasons = append(reasons, "container running")
		return adapters.RuntimeHealthHealthy(strings.Join(reasons, "; ")), nil
	}
	if unit := a.serviceUnit(); unit != "" {
		out, _ := a.runControl(ctx, "systemctl", "is-active", unit)
		if strings.TrimSpace(string(out)) != "active" {
			return adapters.RuntimeHealthDegraded("hermes service " + unit + " is not active"), nil
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
	return strings.TrimSpace(getenv("SIDEPLANE_HERMES_SERVICE_UNIT"))
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
