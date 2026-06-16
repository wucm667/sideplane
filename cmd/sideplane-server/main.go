package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/wucm667/sideplane/internal/server"
)

const version = "dev"

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("sideplane-server %s\n", version)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	httpServer := &http.Server{
		Addr:    *addr,
		Handler: server.NewHandler(),
	}

	logger.Info("starting sideplane-server", "addr", *addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("sideplane-server stopped", "error", err)
		os.Exit(1)
	}
}
