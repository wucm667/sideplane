package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/wucm667/sideplane/internal/server"
	"github.com/wucm667/sideplane/internal/store"
)

const version = "dev"

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "sideplane.db", "SQLite database path")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("sideplane-server %s\n", version)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	nodeStore, err := store.OpenSQLiteNodeStore(context.Background(), *dbPath)
	if err != nil {
		logger.Error("open sqlite store", "db", *dbPath, "error", err)
		os.Exit(1)
	}
	defer nodeStore.Close()

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: server.NewHandlerWithStore(nodeStore),
	}

	logger.Info("starting sideplane-server", "addr", *addr, "db", *dbPath)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("sideplane-server stopped", "error", err)
		os.Exit(1)
	}
}
