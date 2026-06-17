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
	client, err := sidecar.NewHeartbeatClient(sidecar.HeartbeatClientConfig{
		ServerURL:      runtimeConfig.ServerURL,
		NodeID:         runtimeConfig.NodeID,
		NodeCredential: runtimeConfig.NodeCredential,
		SidecarVersion: version,
	})
	if err != nil {
		logger.Error("configure heartbeat client", "error", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info(
		"starting sidecar heartbeat",
		"server", runtimeConfig.ServerURL,
		"node_id", runtimeConfig.NodeID,
		"state", runtimeConfig.StatePath,
		"interval", heartbeatInterval.String(),
	)
	err = sidecar.RunHeartbeatLoop(ctx, client, *heartbeatInterval, func(resp *protocol.HeartbeatResponse, err error) {
		if err != nil {
			logger.Warn("heartbeat failed", "error", err)
			return
		}
		logger.Info("heartbeat accepted", "node_id", resp.Node.NodeID, "state", resp.Node.State)
	})
	if err != nil {
		logger.Error("heartbeat loop stopped", "error", err)
		return 1
	}
	return 0
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
