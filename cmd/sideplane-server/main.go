package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/wucm667/sideplane/internal/server"
	"github.com/wucm667/sideplane/internal/store"
)

const version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane-server", flag.ContinueOnError)
	flags.SetOutput(stderr)

	addr := flags.String("addr", ":8080", "HTTP listen address")
	dbPath := flags.String("db", "sideplane.db", "SQLite database path")
	webDir := flags.String("web-dir", "", "directory of built Web UI static assets to serve; when empty, only the API is served")
	staleAfter := flags.Duration("stale-after", server.DefaultStaleAfter, "duration after last heartbeat before a node is stale")
	offlineAfter := flags.Duration("offline-after", server.DefaultOfflineAfter, "duration after last heartbeat before a node is offline")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintf(stdout, "sideplane-server %s\n", version)
		return 0
	}

	freshness := server.FreshnessPolicy{
		StaleAfter:   *staleAfter,
		OfflineAfter: *offlineAfter,
	}
	if err := freshness.Validate(); err != nil {
		fmt.Fprintf(stderr, "invalid freshness policy: %v\n", err)
		return 1
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	nodeStore, err := store.OpenSQLiteNodeStore(context.Background(), *dbPath)
	if err != nil {
		logger.Error("open sqlite store", "db", *dbPath, "error", err)
		return 1
	}
	defer nodeStore.Close()

	handler, err := server.NewHandlerWithStoreAndFreshnessPolicy(nodeStore, freshness)
	if err != nil {
		logger.Error("configure freshness policy", "error", err)
		return 1
	}

	if *webDir != "" {
		webHandler, err := server.NewWebHandler(*webDir, handler)
		if err != nil {
			logger.Error("configure web-dir", "web_dir", *webDir, "error", err)
			return 1
		}
		handler = webHandler
	}

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: handler,
	}

	logger.Info(
		"starting sideplane-server",
		"addr", *addr,
		"db", *dbPath,
		"web_dir", *webDir,
		"stale_after", staleAfter.String(),
		"offline_after", offlineAfter.String(),
	)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("sideplane-server stopped", "error", err)
		return 1
	}
	return 0
}
