package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
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
	heartbeatInterval := flags.Duration("heartbeat-interval", 30*time.Second, "heartbeat interval")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintf(stdout, "sideplane-sidecar %s\n", version)
		return 0
	}

	if *serverURL == "" {
		fmt.Fprintln(stdout, "sideplane-sidecar skeleton")
		return 0
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	client, err := sidecar.NewHeartbeatClient(sidecar.HeartbeatClientConfig{
		ServerURL:      *serverURL,
		NodeID:         *nodeID,
		SidecarVersion: version,
	})
	if err != nil {
		logger.Error("configure heartbeat client", "error", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting sidecar heartbeat", "server", *serverURL, "interval", heartbeatInterval.String())
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
