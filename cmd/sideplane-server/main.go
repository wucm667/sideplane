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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wucm667/sideplane/internal/auth"
	"github.com/wucm667/sideplane/internal/buildinfo"
	"github.com/wucm667/sideplane/internal/server"
	"github.com/wucm667/sideplane/internal/store"
	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	webassets "github.com/wucm667/sideplane/web"
)

const shutdownTimeout = 10 * time.Second
const heartbeatPruneInterval = 10 * time.Minute
const retentionPruneInterval = time.Hour
const defaultRolloutInterval = 5 * time.Second

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
	heartbeatRetention := flags.Int("heartbeat-retention", store.DefaultHeartbeatRetention, "number of recent heartbeats to keep per node")
	jobRetention := flags.Duration("job-retention", store.DefaultJobRetention, "age to retain completed and failed jobs; set 0 to disable pruning")
	auditRetention := flags.Duration("audit-retention", store.DefaultAuditRetention, "age to retain audit events; set 0 to disable pruning")
	rolloutInterval := flags.Duration("rollout-interval", defaultRolloutInterval, "interval between rollout reconciliation ticks; set 0 to disable")
	enrollmentRateLimit := flags.Int("enrollment-rate-limit", server.DefaultEnrollmentRateLimit, "max enrollment attempts per remote address per rate-limit-window; set 0 to disable")
	operatorAuthRateLimit := flags.Int("operator-auth-rate-limit", server.DefaultOperatorAuthRateLimit, "max failed operator auth attempts per remote address per rate-limit-window; set 0 to disable")
	rateLimitWindow := flags.Duration("rate-limit-window", server.DefaultRateLimitWindow, "fixed window for enrollment and operator auth rate limits")
	operatorTokenFlag := flags.String("operator-token", "", "bearer token required for mutating operator API requests; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	allowUnauthenticatedOperatorAPIFlag := flags.Bool("allow-unauthenticated-operator-api", false, "DEVELOPMENT ONLY: allow mutating operator API requests without an operator token; can also be set with SIDEPLANE_ALLOW_UNAUTHENTICATED_OPERATOR_API=true")
	signingKeyPath := flags.String("signing-key", "", "path to server config-plan signing key; can also be set with SIDEPLANE_SIGNING_KEY")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, buildinfo.Format("sideplane-server"))
		return 0
	}

	setFlags := visitedFlags(flags)
	if err := applyServerEnvFallbacks(setFlags, serverFlagValues{
		addr:                  addr,
		dbPath:                dbPath,
		webDir:                webDir,
		staleAfter:            staleAfter,
		offlineAfter:          offlineAfter,
		heartbeatRetention:    heartbeatRetention,
		jobRetention:          jobRetention,
		auditRetention:        auditRetention,
		rolloutInterval:       rolloutInterval,
		enrollmentRateLimit:   enrollmentRateLimit,
		operatorAuthRateLimit: operatorAuthRateLimit,
		rateLimitWindow:       rateLimitWindow,
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
	if *heartbeatRetention <= 0 {
		fmt.Fprintln(stderr, "invalid heartbeat retention: heartbeat-retention must be positive")
		return 1
	}
	if *jobRetention < 0 {
		fmt.Fprintln(stderr, "invalid job retention: job-retention must be zero or positive")
		return 1
	}
	if *auditRetention < 0 {
		fmt.Fprintln(stderr, "invalid audit retention: audit-retention must be zero or positive")
		return 1
	}
	if *rolloutInterval < 0 {
		fmt.Fprintln(stderr, "invalid rollout interval: rollout-interval must be zero or positive")
		return 1
	}
	if *enrollmentRateLimit < 0 {
		fmt.Fprintln(stderr, "invalid enrollment rate limit: enrollment-rate-limit must be zero or positive")
		return 1
	}
	if *operatorAuthRateLimit < 0 {
		fmt.Fprintln(stderr, "invalid operator auth rate limit: operator-auth-rate-limit must be zero or positive")
		return 1
	}
	if *rateLimitWindow <= 0 {
		fmt.Fprintln(stderr, "invalid rate limit window: rate-limit-window must be positive")
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
	signingKey, err := spcrypto.LoadOrCreateKeyPair(keyPath)
	if err != nil {
		logger.Error("configure signing key", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	schemaVersion, err := nodeStore.SchemaVersion(context.Background())
	if err != nil {
		logger.Error("read sqlite schema version", "db", *dbPath, "error", err)
		return 1
	}

	handler, err := server.NewHandlerWithConfig(server.HandlerConfig{
		Store:                           nodeStore,
		Freshness:                       freshness,
		OperatorToken:                   operatorToken,
		AllowUnauthenticatedOperatorAPI: allowUnauthenticatedOperatorAPI,
		SigningKeyPair:                  signingKey,
		RateLimits: server.RateLimitConfig{
			EnrollmentLimit:     *enrollmentRateLimit,
			OperatorAuthLimit:   *operatorAuthRateLimit,
			Window:              *rateLimitWindow,
			DisableEnrollment:   *enrollmentRateLimit == 0,
			DisableOperatorAuth: *operatorAuthRateLimit == 0,
		},
		Logger: logger,
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

	startHeartbeatPruner(ctx, nodeStore, *heartbeatRetention, heartbeatPruneInterval, logger)
	startRetentionPruner(ctx, nodeStore, *jobRetention, *auditRetention, retentionPruneInterval, logger)
	server.StartRolloutOrchestrator(ctx, server.RolloutOrchestratorConfig{
		Store:      nodeStore,
		Freshness:  freshness,
		SigningKey: signingKey,
		Interval:   *rolloutInterval,
		Logger:     logger,
	})

	logger.Info(
		"starting sideplane-server",
		"addr", *addr,
		"db", *dbPath,
		"web", webMode,
		"stale_after", staleAfter.String(),
		"offline_after", offlineAfter.String(),
		"heartbeat_retention", *heartbeatRetention,
		"job_retention", jobRetention.String(),
		"audit_retention", auditRetention.String(),
		"rollout_interval", rolloutInterval.String(),
		"enrollment_rate_limit", *enrollmentRateLimit,
		"operator_auth_rate_limit", *operatorAuthRateLimit,
		"rate_limit_window", rateLimitWindow.String(),
		"schema_version", schemaVersion,
	)

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
	addr                  *string
	dbPath                *string
	webDir                *string
	staleAfter            *time.Duration
	offlineAfter          *time.Duration
	heartbeatRetention    *int
	jobRetention          *time.Duration
	auditRetention        *time.Duration
	rolloutInterval       *time.Duration
	enrollmentRateLimit   *int
	operatorAuthRateLimit *int
	rateLimitWindow       *time.Duration
}

func startHeartbeatPruner(ctx context.Context, nodeStore store.NodeStore, keep int, interval time.Duration, logger *slog.Logger) {
	if nodeStore == nil || interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				deleted, err := nodeStore.PruneHeartbeats(ctx, keep)
				if err != nil {
					logger.Warn("prune heartbeats failed", "error", err)
					continue
				}
				if deleted > 0 {
					logger.Info("pruned old heartbeats", "deleted", deleted, "keep_per_node", keep)
				}
			}
		}
	}()
}

func startRetentionPruner(ctx context.Context, dataStore store.Store, jobRetention time.Duration, auditRetention time.Duration, interval time.Duration, logger *slog.Logger) {
	if dataStore == nil || interval <= 0 {
		return
	}
	if jobRetention == 0 && auditRetention == 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now().UTC()
				if jobRetention > 0 {
					deleted, err := dataStore.PruneTerminalJobs(ctx, now.Add(-jobRetention))
					if err != nil {
						logger.Warn("prune terminal jobs failed", "error", err)
					} else if deleted > 0 {
						logger.Info("pruned old terminal jobs", "deleted", deleted, "retention", jobRetention.String())
					}
				}
				if auditRetention > 0 {
					deleted, err := dataStore.PruneAuditEvents(ctx, now.Add(-auditRetention))
					if err != nil {
						logger.Warn("prune audit events failed", "error", err)
					} else if deleted > 0 {
						logger.Info("pruned old audit events", "deleted", deleted, "retention", auditRetention.String())
					}
				}
			}
		}
	}()
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
	if err := applyIntEnvFallback(setFlags, "heartbeat-retention", "SIDEPLANE_HEARTBEAT_RETENTION", values.heartbeatRetention); err != nil {
		return err
	}
	if err := applyDurationEnvFallback(setFlags, "job-retention", "SIDEPLANE_JOB_RETENTION", values.jobRetention); err != nil {
		return err
	}
	if err := applyDurationEnvFallback(setFlags, "audit-retention", "SIDEPLANE_AUDIT_RETENTION", values.auditRetention); err != nil {
		return err
	}
	if err := applyDurationEnvFallback(setFlags, "rollout-interval", "SIDEPLANE_ROLLOUT_INTERVAL", values.rolloutInterval); err != nil {
		return err
	}
	if err := applyIntEnvFallback(setFlags, "enrollment-rate-limit", "SIDEPLANE_ENROLLMENT_RATE_LIMIT", values.enrollmentRateLimit); err != nil {
		return err
	}
	if err := applyIntEnvFallback(setFlags, "operator-auth-rate-limit", "SIDEPLANE_OPERATOR_AUTH_RATE_LIMIT", values.operatorAuthRateLimit); err != nil {
		return err
	}
	if err := applyDurationEnvFallback(setFlags, "rate-limit-window", "SIDEPLANE_RATE_LIMIT_WINDOW", values.rateLimitWindow); err != nil {
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

func applyIntEnvFallback(setFlags map[string]bool, flagName string, envName string, value *int) error {
	if value == nil || setFlags[flagName] {
		return nil
	}
	envValue := strings.TrimSpace(os.Getenv(envName))
	if envValue == "" {
		return nil
	}
	parsed, err := strconv.Atoi(envValue)
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
