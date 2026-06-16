package main

import (
	"context"
	"flag"
	"fmt"
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
	serverURL := flag.String("server", "", "Sideplane server URL")
	nodeID := flag.String("node-id", "", "node ID to report in heartbeats")
	heartbeatInterval := flag.Duration("heartbeat-interval", 30*time.Second, "heartbeat interval")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("sideplane-sidecar %s\n", version)
		return
	}

	if *serverURL == "" {
		fmt.Fprintln(os.Stdout, "sideplane-sidecar skeleton")
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	client, err := sidecar.NewHeartbeatClient(sidecar.HeartbeatClientConfig{
		ServerURL:      *serverURL,
		NodeID:         *nodeID,
		SidecarVersion: version,
	})
	if err != nil {
		logger.Error("configure heartbeat client", "error", err)
		os.Exit(1)
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
		os.Exit(1)
	}
}
