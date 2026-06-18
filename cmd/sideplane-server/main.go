package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/wucm667/sideplane/internal/auth"
	"github.com/wucm667/sideplane/internal/server"
	"github.com/wucm667/sideplane/internal/store"
	webassets "github.com/wucm667/sideplane/web"
)

const version = "dev"
const shutdownTimeout = 10 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane-server", flag.ContinueOnError)
	flags.SetOutput(stderr)

	addr := flags.String("addr", ":8080", "HTTP listen address")
	dbPath := flags.String("db", "sideplane.db", "SQLite database path")
	webDir := flags.String("web-dir", "", "directory of built Web UI static assets to serve; overrides embedded assets when set")
	staleAfter := flags.Duration("stale-after", server.DefaultStaleAfter, "duration after last heartbeat before a node is stale")
	offlineAfter := flags.Duration("offline-after", server.DefaultOfflineAfter, "duration after last heartbeat before a node is offline")
	operatorTokenFlag := flags.String("operator-token", "", "bearer token required for mutating operator API requests; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	allowUnauthenticatedOperatorAPIFlag := flags.Bool("allow-unauthenticated-operator-api", false, "DEVELOPMENT ONLY: allow mutating operator API requests without an operator token; can also be set with SIDEPLANE_ALLOW_UNAUTHENTICATED_OPERATOR_API=true")
	signingKeyPath := flags.String("signing-key", "", "path to server config-plan signing key; can also be set with SIDEPLANE_SIGNING_KEY")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintf(stdout, "sideplane-server %s\n", version)
		return 0
	}

	setFlags := visitedFlags(flags)
	if err := applyServerEnvFallbacks(setFlags, serverFlagValues{
		addr:         addr,
		dbPath:       dbPath,
		webDir:       webDir,
		staleAfter:   staleAfter,
		offlineAfter: offlineAfter,
	}); err != nil {
		fmt.Fprintf(stderr, "invalid environment configuration: %v\n", err)
		return 1
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
	operatorToken := strings.TrimSpace(*operatorTokenFlag)
	if operatorToken == "" {
		operatorToken = strings.TrimSpace(os.Getenv(auth.OperatorTokenEnv))
	}
	allowUnauthenticatedOperatorAPI := *allowUnauthenticatedOperatorAPIFlag || truthyEnv(os.Getenv(auth.AllowUnauthenticatedOperatorAPIEnv))
	if operatorToken == "" {
		if allowUnauthenticatedOperatorAPI {
			logger.Warn("operator token not configured; explicit development mode allows unauthenticated mutating operator endpoints")
		} else {
			logger.Warn("operator token not configured; mutating operator endpoints will reject requests")
		}
	}
	keyPath := strings.TrimSpace(*signingKeyPath)
	if keyPath == "" {
		keyPath = strings.TrimSpace(os.Getenv("SIDEPLANE_SIGNING_KEY"))
	}
	if keyPath == "" {
		logger.Warn("signing key not configured; config apply plans will use an ephemeral in-memory key; set SIDEPLANE_SIGNING_KEY or --signing-key for apply-capable deployments")
	}

	nodeStore, err := store.OpenSQLiteNodeStore(context.Background(), *dbPath)
	if err != nil {
		logger.Error("open sqlite store", "db", *dbPath, "error", err)
		return 1
	}
	defer func() {
		if err := nodeStore.Close(); err != nil {
			logger.Error("close sqlite store", "db", *dbPath, "error", err)
		}
	}()

	handler, err := server.NewHandlerWithConfig(server.HandlerConfig{
		Store:                           nodeStore,
		Freshness:                       freshness,
		OperatorToken:                   operatorToken,
		AllowUnauthenticatedOperatorAPI: allowUnauthenticatedOperatorAPI,
		SigningKeyPath:                  keyPath,
		Logger:                          logger,
	})
	if err != nil {
		logger.Error("configure freshness policy", "error", err)
		return 1
	}

	webMode := "embedded"
	if *webDir != "" {
		webHandler, err := server.NewWebHandler(*webDir, handler)
		if err != nil {
			logger.Error("configure web-dir", "web_dir", *webDir, "error", err)
			return 1
		}
		handler = webHandler
		webMode = *webDir
	} else {
		distFS, err := fs.Sub(webassets.Assets, "dist")
		if err != nil {
			logger.Error("configure embedded web assets", "error", err)
			return 1
		}
		handler = server.NewEmbeddedWebHandler(distFS, handler)
	}

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: handler,
	}

	logger.Info(
		"starting sideplane-server",
		"addr", *addr,
		"db", *dbPath,
		"web", webMode,
		"stale_after", staleAfter.String(),
		"offline_after", offlineAfter.String(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("sideplane-server stopped", "error", err)
			return 1
		}
	case <-ctx.Done():
		logger.Info("shutting down sideplane-server", "timeout", shutdownTimeout.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown sideplane-server", "error", err)
			return 1
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("sideplane-server stopped", "error", err)
			return 1
		}
	}
	logger.Info("sideplane-server stopped")
	return 0
}

type serverFlagValues struct {
	addr         *string
	dbPath       *string
	webDir       *string
	staleAfter   *time.Duration
	offlineAfter *time.Duration
}

func visitedFlags(flags *flag.FlagSet) map[string]bool {
	visited := map[string]bool{}
	flags.Visit(func(f *flag.Flag) {
		visited[f.Name] = true
	})
	return visited
}

func applyServerEnvFallbacks(setFlags map[string]bool, values serverFlagValues) error {
	applyStringEnvFallback(setFlags, "addr", "SIDEPLANE_ADDR", values.addr)
	applyStringEnvFallback(setFlags, "db", "SIDEPLANE_DB_PATH", values.dbPath)
	applyStringEnvFallback(setFlags, "web-dir", "SIDEPLANE_WEB_DIR", values.webDir)
	if err := applyDurationEnvFallback(setFlags, "stale-after", "SIDEPLANE_STALE_AFTER", values.staleAfter); err != nil {
		return err
	}
	if err := applyDurationEnvFallback(setFlags, "offline-after", "SIDEPLANE_OFFLINE_AFTER", values.offlineAfter); err != nil {
		return err
	}
	return nil
}

func applyStringEnvFallback(setFlags map[string]bool, flagName string, envName string, value *string) {
	if value == nil || setFlags[flagName] {
		return
	}
	if envValue := strings.TrimSpace(os.Getenv(envName)); envValue != "" {
		*value = envValue
	}
}

func applyDurationEnvFallback(setFlags map[string]bool, flagName string, envName string, value *time.Duration) error {
	if value == nil || setFlags[flagName] {
		return nil
	}
	envValue := strings.TrimSpace(os.Getenv(envName))
	if envValue == "" {
		return nil
	}
	parsed, err := time.ParseDuration(envValue)
	if err != nil {
		return fmt.Errorf("%s: %w", envName, err)
	}
	*value = parsed
	return nil
}

func truthyEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}
