package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/wucm667/sideplane/internal/sidecar"
	"github.com/wucm667/sideplane/pkg/adapters/hermes"
	"github.com/wucm667/sideplane/pkg/adapters/openclaw"
	"github.com/wucm667/sideplane/pkg/adapters/registry"
	"github.com/wucm667/sideplane/pkg/protocol"
)

const version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "enroll" {
		return runEnroll(args[1:], stdout, stderr)
	}

	flags := flag.NewFlagSet("sideplane-sidecar", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL")
	nodeID := flags.String("node-id", "", "node ID to report in heartbeats")
	nodeCredential := flags.String("node-credential", "", "node credential for testing or temporary runs")
	statePath := flags.String("state", "", "sidecar state file path")
	heartbeatInterval := flags.Duration("heartbeat-interval", 30*time.Second, "heartbeat interval")
	jobPollInterval := flags.Duration("job-poll-interval", 30*time.Second, "job poll interval")
	hermesConfigPaths := flags.String("hermes-config-paths", "", "path-list of read-only Hermes config files to inspect; can also be set with SIDEPLANE_HERMES_CONFIG_PATHS")
	hermesDockerContainer := flags.String("hermes-docker-container", "", "optional read-only Docker container name for Hermes status/log inspection; can also be set with SIDEPLANE_HERMES_DOCKER_CONTAINER")
	serverPublicKey := flags.String("server-public-key", "", "base64 ed25519 server public key for signed config plans")
	applyWorkDir := flags.String("apply-work-dir", "", "sidecar-controlled work directory for config apply dry runs")
	allowLiveApply := flags.Bool("allow-live-apply", false, "DANGEROUS: allow live config replace and restart; off by default (dry-run only)")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintf(stdout, "sideplane-sidecar %s\n", version)
		return 0
	}

	runtimeConfig, err := resolveRuntimeConfig(*serverURL, *nodeID, *nodeCredential, *statePath)
	if err != nil {
		fmt.Fprintf(stderr, "load sidecar state: %v\n", err)
		return 1
	}
	if runtimeConfig.NodeCredential == "" {
		fmt.Fprintln(stderr, "node credential is required; run sideplane-sidecar enroll first")
		return 1
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	if *allowLiveApply {
		logger.Warn("live config apply is ENABLED; this sidecar may replace config and restart services")
	}

	hermesOptions := []hermes.Option{}
	if value := strings.TrimSpace(*hermesConfigPaths); value != "" {
		hermesOptions = append(hermesOptions, hermes.WithConfigPaths(splitPathList(value)...))
	}
	if value := strings.TrimSpace(*hermesDockerContainer); value != "" {
		hermesOptions = append(hermesOptions, hermes.WithDockerContainer(value))
	}
	reg := registry.New(hermes.NewAdapter(hermesOptions...), openclaw.NewAdapter())

	client, err := sidecar.NewHeartbeatClient(sidecar.HeartbeatClientConfig{
		ServerURL:      runtimeConfig.ServerURL,
		NodeID:         runtimeConfig.NodeID,
		NodeCredential: runtimeConfig.NodeCredential,
		SidecarVersion: version,
		Collector:      reg,
	})
	if err != nil {
		logger.Error("configure heartbeat client", "error", err)
		return 1
	}

	jobPoller, err := sidecar.NewJobPoller(sidecar.JobPollerConfig{
		ServerURL:      runtimeConfig.ServerURL,
		NodeID:         runtimeConfig.NodeID,
		NodeCredential: runtimeConfig.NodeCredential,
		PublicKey:      *serverPublicKey,
		ApplyWorkDir:   *applyWorkDir,
		AllowLiveApply: *allowLiveApply,
		Collector:      reg,
		Logger:         logger,
	})
	if err != nil {
		logger.Error("configure job poller", "error", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info(
		"starting sidecar",
		"server", runtimeConfig.ServerURL,
		"node_id", runtimeConfig.NodeID,
		"state", runtimeConfig.StatePath,
		"heartbeat_interval", heartbeatInterval.String(),
		"job_poll_interval", jobPollInterval.String(),
	)

	errCh := make(chan error, 2)
	go func() {
		errCh <- sidecar.RunHeartbeatLoop(ctx, client, *heartbeatInterval, func(resp *protocol.HeartbeatResponse, err error) {
			if err != nil {
				logger.Warn("heartbeat failed", "error", err)
				return
			}
			logger.Info("heartbeat accepted", "node_id", resp.Node.NodeID, "state", resp.Node.State)
		})
	}()
	go func() {
		errCh <- sidecar.RunJobPoller(ctx, jobPoller, *jobPollInterval)
	}()

	if err := <-errCh; err != nil && ctx.Err() == nil {
		logger.Error("sidecar loop stopped", "error", err)
		return 1
	}
	return 0
}

func splitPathList(raw string) []string {
	replacer := strings.NewReplacer("\n", string(os.PathListSeparator), ",", string(os.PathListSeparator))
	parts := strings.Split(replacer.Replace(raw), string(os.PathListSeparator))
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if path := strings.TrimSpace(part); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

type runtimeConfig struct {
	ServerURL      string
	NodeID         string
	NodeCredential string
	StatePath      string
}

func resolveRuntimeConfig(serverURLFlag string, nodeIDFlag string, nodeCredentialFlag string, statePathFlag string) (runtimeConfig, error) {
	cfg := runtimeConfig{}

	statePath := strings.TrimSpace(statePathFlag)
	if statePath == "" {
		defaultPath, err := sidecar.DefaultStatePath()
		if err != nil {
			return cfg, fmt.Errorf("resolve default state path: %w", err)
		}
		statePath = defaultPath
	}
	cfg.StatePath = statePath

	if _, err := os.Stat(statePath); err == nil {
		state, err := sidecar.ReadState(statePath)
		if err != nil {
			return cfg, err
		}
		cfg.ServerURL = strings.TrimSpace(state.ServerURL)
		cfg.NodeID = strings.TrimSpace(state.NodeID)
		cfg.NodeCredential = strings.TrimSpace(state.NodeCredential)
	} else if !os.IsNotExist(err) {
		return cfg, fmt.Errorf("stat sidecar state: %w", err)
	}

	if value := strings.TrimSpace(serverURLFlag); value != "" {
		cfg.ServerURL = value
	}
	if value := strings.TrimSpace(nodeIDFlag); value != "" {
		cfg.NodeID = value
	}
	if cfg.NodeCredential == "" {
		cfg.NodeCredential = strings.TrimSpace(nodeCredentialFlag)
	}

	return cfg, nil
}

func runEnroll(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane-sidecar enroll", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL")
	token := flags.String("token", "", "one-time enrollment token")
	nodeID := flags.String("node-id", "", "optional node ID to register")
	statePath := flags.String("state", "", "sidecar state file path")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *statePath == "" {
		path, err := sidecar.DefaultStatePath()
		if err != nil {
			fmt.Fprintf(stderr, "resolve default state path: %v\n", err)
			return 1
		}
		*statePath = path
	}

	client, err := sidecar.NewEnrollmentClient(sidecar.EnrollmentClientConfig{
		ServerURL:      *serverURL,
		NodeID:         *nodeID,
		Token:          *token,
		SidecarVersion: version,
	})
	if err != nil {
		fmt.Fprintf(stderr, "configure enrollment client: %v\n", err)
		return 1
	}

	resp, err := client.Enroll(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "enroll node: %v\n", err)
		return 1
	}

	if err := sidecar.WriteState(*statePath, sidecar.SidecarState{
		ServerURL:      *serverURL,
		NodeID:         resp.NodeID,
		NodeCredential: resp.NodeCredential,
		EnrolledAt:     time.Now().UTC(),
	}); err != nil {
		fmt.Fprintf(stderr, "write sidecar state: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "enrolled node %s\n", resp.NodeID)
	fmt.Fprintf(stdout, "state: %s\n", *statePath)
	return 0
}
