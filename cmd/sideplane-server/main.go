package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
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
const defaultBackupRetention = 7
const backupFilePrefix = "sideplane-backup-"
const backupFileTimeLayout = "20060102T150405.000000000"
const serverConfigEnv = "SIDEPLANE_SERVER_CONFIG"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func isHelpArg(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func commandHelpRequested(args []string) bool {
	for _, arg := range args {
		if isHelpArg(arg) {
			return true
		}
	}
	return false
}

func printCommandHelp(w io.Writer, usage string, flags *flag.FlagSet) {
	fmt.Fprintf(w, "usage: %s\n\n", usage)
	flags.SetOutput(w)
	flags.PrintDefaults()
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "config-file" {
		if len(args) >= 2 && args[1] == "path" {
			return runServerConfigFilePath(args[2:], stdout, stderr)
		}
		fmt.Fprintln(stderr, "usage: sideplane-server config-file path [--config PATH]")
		return 1
	}
	if len(args) > 0 && args[0] == "backup" {
		return runServerBackup(args[1:], stdout, stderr)
	}

	flags := flag.NewFlagSet("sideplane-server", flag.ContinueOnError)
	flags.SetOutput(stderr)

	configPathFlag := flags.String("config", "", "server config file path; can also be set with SIDEPLANE_SERVER_CONFIG")
	addr := flags.String("addr", ":8080", "HTTP listen address")
	dbPath := flags.String("db", "sideplane.db", "SQLite database path")
	tlsCert := flags.String("tls-cert", "", "path to TLS certificate file; can also be set with SIDEPLANE_TLS_CERT")
	tlsKey := flags.String("tls-key", "", "path to TLS private key file; can also be set with SIDEPLANE_TLS_KEY")
	tlsRedirectAddr := flags.String("tls-redirect-addr", "", "optional HTTP listen address that redirects requests to HTTPS; can also be set with SIDEPLANE_TLS_REDIRECT_ADDR")
	basePath := flags.String("base-path", "", "optional URL path prefix to serve the app under; can also be set with SIDEPLANE_BASE_PATH")
	backupDir := flags.String("backup-dir", "", "directory for periodic online DB backups; periodic backups are off unless set with a positive --backup-interval; can also be set with SIDEPLANE_BACKUP_DIR")
	backupInterval := flags.Duration("backup-interval", 0, "interval between periodic online DB backups; set 0 to disable; can also be set with SIDEPLANE_BACKUP_INTERVAL")
	backupRetention := flags.Int("backup-retention", defaultBackupRetention, "number of recent periodic DB backups to retain; can also be set with SIDEPLANE_BACKUP_RETENTION")
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
	values := serverFlagValues{
		addr:                  addr,
		dbPath:                dbPath,
		tlsCert:               tlsCert,
		tlsKey:                tlsKey,
		tlsRedirectAddr:       tlsRedirectAddr,
		basePath:              basePath,
		backupDir:             backupDir,
		backupInterval:        backupInterval,
		backupRetention:       backupRetention,
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
		allowUnauthenticated:  allowUnauthenticatedOperatorAPIFlag,
	}
	configPath, configRequired := resolvedServerConfigPath(*configPathFlag)
	fileConfig, err := loadServerConfigFile(configPath, configRequired)
	if err != nil {
		fmt.Fprintf(stderr, "invalid server config file: %v\n", err)
		return 1
	}
	if err := applyServerConfigFallbacks(setFlags, fileConfig, values); err != nil {
		fmt.Fprintf(stderr, "invalid server config file: %v\n", err)
		return 1
	}
	if err := applyServerEnvFallbacks(setFlags, values); err != nil {
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
	if *backupInterval < 0 {
		fmt.Fprintln(stderr, "invalid backup interval: backup-interval must be zero or positive")
		return 1
	}
	if *backupRetention <= 0 {
		fmt.Fprintln(stderr, "invalid backup retention: backup-retention must be positive")
		return 1
	}
	if *backupInterval > 0 && strings.TrimSpace(*backupDir) == "" {
		fmt.Fprintln(stderr, "invalid backup configuration: backup-dir is required when backup-interval is set")
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
	tlsConfig, err := validateServerTLS(*tlsCert, *tlsKey)
	if err != nil {
		fmt.Fprintf(stderr, "invalid TLS configuration: %v\n", err)
		return 1
	}
	tlsRedirectAddrValue := strings.TrimSpace(*tlsRedirectAddr)
	if tlsRedirectAddrValue != "" && !tlsConfig.Enabled() {
		fmt.Fprintln(stderr, "invalid TLS redirect configuration: tls-redirect-addr requires tls-cert and tls-key")
		return 1
	}
	normalizedBasePath, err := server.NormalizeBasePath(*basePath)
	if err != nil {
		fmt.Fprintf(stderr, "invalid base path: %v\n", err)
		return 1
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	operatorToken := strings.TrimSpace(*operatorTokenFlag)
	if operatorToken == "" {
		operatorToken = strings.TrimSpace(os.Getenv(auth.OperatorTokenEnv))
	}
	allowUnauthenticatedOperatorAPI := *allowUnauthenticatedOperatorAPIFlag
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
	metrics := server.NewMetrics()

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
		Metrics:       metrics,
		SchemaVersion: schemaVersion,
		Logger:        logger,
	})
	if err != nil {
		logger.Error("configure freshness policy", "error", err)
		return 1
	}

	webMode := "embedded"
	if *webDir != "" {
		webHandler, err := server.NewWebHandlerWithBase(*webDir, handler, normalizedBasePath)
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
		handler = server.NewEmbeddedWebHandlerWithBase(distFS, handler, normalizedBasePath)
	}
	handler, err = server.NewBasePathHandler(normalizedBasePath, handler)
	if err != nil {
		logger.Error("configure base path", "base_path", normalizedBasePath, "error", err)
		return 1
	}

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: handler,
	}
	var redirectServer *http.Server
	if tlsRedirectAddrValue != "" {
		redirectServer = newTLSRedirectServer(tlsRedirectAddrValue, *addr)
	}
	scheme := "http"
	if tlsConfig.Enabled() {
		scheme = "https"
	}

	startHeartbeatPruner(ctx, nodeStore, *heartbeatRetention, heartbeatPruneInterval, logger)
	startRetentionPruner(ctx, nodeStore, *jobRetention, *auditRetention, retentionPruneInterval, logger)
	startBackupScheduler(ctx, nodeStore, strings.TrimSpace(*backupDir), *backupInterval, *backupRetention, logger)
	server.StartRolloutOrchestrator(ctx, server.RolloutOrchestratorConfig{
		Store:      nodeStore,
		Freshness:  freshness,
		SigningKey: signingKey,
		Metrics:    metrics,
		Interval:   *rolloutInterval,
		Logger:     logger,
	})
	server.StartAlertDispatcher(ctx, server.AlertDispatcherConfig{
		Store:   nodeStore,
		Metrics: metrics,
		Logger:  logger,
	})

	logger.Info(
		"starting sideplane-server",
		"addr", *addr,
		"scheme", scheme,
		"tls_redirect_addr", tlsRedirectAddrValue,
		"base_path", normalizedBasePath,
		"db", *dbPath,
		"web", webMode,
		"stale_after", staleAfter.String(),
		"offline_after", offlineAfter.String(),
		"heartbeat_retention", *heartbeatRetention,
		"job_retention", jobRetention.String(),
		"audit_retention", auditRetention.String(),
		"rollout_interval", rolloutInterval.String(),
		"backup_dir", *backupDir,
		"backup_interval", backupInterval.String(),
		"enrollment_rate_limit", *enrollmentRateLimit,
		"operator_auth_rate_limit", *operatorAuthRateLimit,
		"rate_limit_window", rateLimitWindow.String(),
		"schema_version", schemaVersion,
	)

	if err := serveHTTPServer(ctx, httpServer, redirectServer, tlsConfig, logger); err != nil {
		return 1
	}
	logger.Info("sideplane-server stopped")
	return 0
}

type serverTLSConfig struct {
	CertFile string
	KeyFile  string
}

func (c serverTLSConfig) Enabled() bool {
	return c.CertFile != "" && c.KeyFile != ""
}

type serverFlagValues struct {
	addr                  *string
	dbPath                *string
	tlsCert               *string
	tlsKey                *string
	tlsRedirectAddr       *string
	basePath              *string
	backupDir             *string
	backupInterval        *time.Duration
	backupRetention       *int
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
	allowUnauthenticated  *bool
}

type serverFileConfig map[string]string

func runServerConfigFilePath(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane-server config-file path", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPathFlag := flags.String("config", "", "server config file path; can also be set with SIDEPLANE_SERVER_CONFIG")
	usage := "sideplane-server config-file path [--config PATH]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	path, _ := resolvedServerConfigPath(*configPathFlag)
	fmt.Fprintln(stdout, path)
	return 0
}

// runServerBackup writes a one-shot online backup of the database and exits
// without serving HTTP.
func runServerBackup(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane-server backup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "sideplane.db", "SQLite database path")
	out := flags.String("out", "", "destination path for the backup copy")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	dest := strings.TrimSpace(*out)
	if dest == "" {
		fmt.Fprintln(stderr, "backup: --out is required")
		return 1
	}

	ctx := context.Background()
	nodeStore, err := store.OpenSQLiteNodeStore(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "backup: open db %s: %v\n", *dbPath, err)
		return 1
	}
	defer func() { _ = nodeStore.Close() }()

	if err := nodeStore.BackupTo(ctx, dest); err != nil {
		fmt.Fprintf(stderr, "backup: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Backup written to %s\n", dest)
	return 0
}

// startBackupScheduler periodically writes online DB backups into dir and prunes
// all but the most recent keep backups. It is off unless dir and a positive
// interval are both set.
func startBackupScheduler(ctx context.Context, backupStore store.OnlineBackupStore, dir string, interval time.Duration, keep int, logger *slog.Logger) {
	if backupStore == nil || dir == "" || interval <= 0 {
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
				dest, pruned, err := performScheduledBackup(ctx, backupStore, dir, keep, time.Now())
				if err != nil {
					logger.Warn("scheduled db backup failed", "error", err)
					continue
				}
				logger.Info("wrote scheduled db backup", "path", dest, "pruned", pruned)
			}
		}
	}()
}

// performScheduledBackup writes one timestamped backup into dir and prunes old
// backups, returning the new backup path and the number of pruned files.
func performScheduledBackup(ctx context.Context, backupStore store.OnlineBackupStore, dir string, keep int, now time.Time) (string, int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, fmt.Errorf("create backup dir: %w", err)
	}
	dest := filepath.Join(dir, backupFilePrefix+now.UTC().Format(backupFileTimeLayout)+".db")
	if err := backupStore.BackupTo(ctx, dest); err != nil {
		return "", 0, err
	}
	pruned, err := pruneBackups(dir, keep)
	if err != nil {
		return dest, pruned, err
	}
	return dest, pruned, nil
}

// pruneBackups keeps the newest keep backup files in dir (named by sortable
// timestamp) and removes the rest, returning the number removed.
func pruneBackups(dir string, keep int) (int, error) {
	if keep <= 0 {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read backup dir: %w", err)
	}
	var backups []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasPrefix(name, backupFilePrefix) && strings.HasSuffix(name, ".db") {
			backups = append(backups, name)
		}
	}
	if len(backups) <= keep {
		return 0, nil
	}
	sort.Strings(backups)
	removed := 0
	for _, name := range backups[:len(backups)-keep] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return removed, fmt.Errorf("remove old backup: %w", err)
		}
		removed++
	}
	return removed, nil
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

func resolvedServerConfigPath(flagValue string) (string, bool) {
	if value := strings.TrimSpace(flagValue); value != "" {
		return expandHomePath(value), true
	}
	if value := strings.TrimSpace(os.Getenv(serverConfigEnv)); value != "" {
		return expandHomePath(value), true
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".config", "sideplane", "server.yaml"), false
	}
	return filepath.Join(home, ".config", "sideplane", "server.yaml"), false
}

func expandHomePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func loadServerConfigFile(path string, required bool) (serverFileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !required {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := parseServerConfig(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func parseServerConfig(data []byte) (serverFileConfig, error) {
	cfg := serverFileConfig{}
	for lineNo, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key: value", lineNo+1)
		}
		canonical, ok := canonicalServerConfigKey(key)
		if !ok {
			return nil, fmt.Errorf("line %d: unsupported key %q", lineNo+1, strings.TrimSpace(key))
		}
		cfg[canonical] = trimServerConfigValue(value)
	}
	return cfg, nil
}

func canonicalServerConfigKey(key string) (string, bool) {
	switch normalizeServerConfigKey(key) {
	case "addr":
		return "addr", true
	case "db", "dbpath":
		return "db", true
	case "tlscert":
		return "tls-cert", true
	case "tlskey":
		return "tls-key", true
	case "tlsredirectaddr":
		return "tls-redirect-addr", true
	case "basepath":
		return "base-path", true
	case "backupdir":
		return "backup-dir", true
	case "backupinterval":
		return "backup-interval", true
	case "backupretention":
		return "backup-retention", true
	case "webdir":
		return "web-dir", true
	case "stale", "staleafter":
		return "stale-after", true
	case "offline", "offlineafter":
		return "offline-after", true
	case "heartbeatretention":
		return "heartbeat-retention", true
	case "jobretention":
		return "job-retention", true
	case "auditretention":
		return "audit-retention", true
	case "rolloutinterval":
		return "rollout-interval", true
	case "enrollmentratelimit":
		return "enrollment-rate-limit", true
	case "operatorauthratelimit":
		return "operator-auth-rate-limit", true
	case "ratelimitwindow":
		return "rate-limit-window", true
	case "allowunauthenticatedoperatorapi":
		return "allow-unauthenticated-operator-api", true
	default:
		return "", false
	}
}

func normalizeServerConfigKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "_", "")
	key = strings.ReplaceAll(key, "-", "")
	return key
}

func trimServerConfigValue(value string) string {
	value = strings.TrimSpace(value)
	if comment := strings.Index(value, " #"); comment >= 0 {
		value = strings.TrimSpace(value[:comment])
	}
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	return strings.TrimSpace(value)
}

func applyServerConfigFallbacks(setFlags map[string]bool, cfg serverFileConfig, values serverFlagValues) error {
	for key, value := range cfg {
		if setFlags[key] {
			continue
		}
		switch key {
		case "addr":
			*values.addr = value
		case "db":
			*values.dbPath = value
		case "tls-cert":
			*values.tlsCert = value
		case "tls-key":
			*values.tlsKey = value
		case "tls-redirect-addr":
			*values.tlsRedirectAddr = value
		case "base-path":
			*values.basePath = value
		case "backup-dir":
			*values.backupDir = value
		case "backup-interval":
			parsed, err := parseServerConfigDuration(key, value)
			if err != nil {
				return err
			}
			*values.backupInterval = parsed
		case "backup-retention":
			parsed, err := parseServerConfigInt(key, value)
			if err != nil {
				return err
			}
			*values.backupRetention = parsed
		case "web-dir":
			*values.webDir = value
		case "stale-after":
			parsed, err := parseServerConfigDuration(key, value)
			if err != nil {
				return err
			}
			*values.staleAfter = parsed
		case "offline-after":
			parsed, err := parseServerConfigDuration(key, value)
			if err != nil {
				return err
			}
			*values.offlineAfter = parsed
		case "heartbeat-retention":
			parsed, err := parseServerConfigInt(key, value)
			if err != nil {
				return err
			}
			*values.heartbeatRetention = parsed
		case "job-retention":
			parsed, err := parseServerConfigDuration(key, value)
			if err != nil {
				return err
			}
			*values.jobRetention = parsed
		case "audit-retention":
			parsed, err := parseServerConfigDuration(key, value)
			if err != nil {
				return err
			}
			*values.auditRetention = parsed
		case "rollout-interval":
			parsed, err := parseServerConfigDuration(key, value)
			if err != nil {
				return err
			}
			*values.rolloutInterval = parsed
		case "enrollment-rate-limit":
			parsed, err := parseServerConfigInt(key, value)
			if err != nil {
				return err
			}
			*values.enrollmentRateLimit = parsed
		case "operator-auth-rate-limit":
			parsed, err := parseServerConfigInt(key, value)
			if err != nil {
				return err
			}
			*values.operatorAuthRateLimit = parsed
		case "rate-limit-window":
			parsed, err := parseServerConfigDuration(key, value)
			if err != nil {
				return err
			}
			*values.rateLimitWindow = parsed
		case "allow-unauthenticated-operator-api":
			parsed, err := parseServerConfigBool(key, value)
			if err != nil {
				return err
			}
			*values.allowUnauthenticated = parsed
		}
	}
	return nil
}

func applyServerEnvFallbacks(setFlags map[string]bool, values serverFlagValues) error {
	applyStringEnvFallback(setFlags, "addr", "SIDEPLANE_ADDR", values.addr)
	applyStringEnvFallback(setFlags, "db", "SIDEPLANE_DB_PATH", values.dbPath)
	applyStringEnvFallback(setFlags, "tls-cert", "SIDEPLANE_TLS_CERT", values.tlsCert)
	applyStringEnvFallback(setFlags, "tls-key", "SIDEPLANE_TLS_KEY", values.tlsKey)
	applyStringEnvFallback(setFlags, "tls-redirect-addr", "SIDEPLANE_TLS_REDIRECT_ADDR", values.tlsRedirectAddr)
	applyStringEnvFallback(setFlags, "base-path", "SIDEPLANE_BASE_PATH", values.basePath)
	applyStringEnvFallback(setFlags, "backup-dir", "SIDEPLANE_BACKUP_DIR", values.backupDir)
	if err := applyDurationEnvFallback(setFlags, "backup-interval", "SIDEPLANE_BACKUP_INTERVAL", values.backupInterval); err != nil {
		return err
	}
	if err := applyIntEnvFallback(setFlags, "backup-retention", "SIDEPLANE_BACKUP_RETENTION", values.backupRetention); err != nil {
		return err
	}
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
	if err := applyBoolEnvFallback(setFlags, "allow-unauthenticated-operator-api", auth.AllowUnauthenticatedOperatorAPIEnv, values.allowUnauthenticated); err != nil {
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

func validateServerTLS(certFile string, keyFile string) (serverTLSConfig, error) {
	certFile = strings.TrimSpace(certFile)
	keyFile = strings.TrimSpace(keyFile)
	if certFile == "" && keyFile == "" {
		return serverTLSConfig{}, nil
	}
	if certFile == "" || keyFile == "" {
		return serverTLSConfig{}, errors.New("tls-cert and tls-key must be set together")
	}
	if _, err := os.Stat(certFile); err != nil {
		return serverTLSConfig{}, fmt.Errorf("read tls cert: %w", err)
	}
	if _, err := os.Stat(keyFile); err != nil {
		return serverTLSConfig{}, fmt.Errorf("read tls key: %w", err)
	}
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		return serverTLSConfig{}, fmt.Errorf("load tls cert/key pair: %w", err)
	}
	return serverTLSConfig{CertFile: certFile, KeyFile: keyFile}, nil
}

func serveHTTPServer(ctx context.Context, httpServer *http.Server, redirectServer *http.Server, tlsConfig serverTLSConfig, logger *slog.Logger) error {
	errCh := make(chan error, 2)
	go func() {
		if tlsConfig.Enabled() {
			errCh <- httpServer.ListenAndServeTLS(tlsConfig.CertFile, tlsConfig.KeyFile)
			return
		}
		errCh <- httpServer.ListenAndServe()
	}()
	if redirectServer != nil {
		go func() {
			errCh <- redirectServer.ListenAndServe()
		}()
	}

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("sideplane-server stopped", "error", err)
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			_ = httpServer.Shutdown(shutdownCtx)
			if redirectServer != nil {
				_ = redirectServer.Shutdown(shutdownCtx)
			}
			return err
		}
	case <-ctx.Done():
		logger.Info("shutting down sideplane-server", "timeout", shutdownTimeout.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown sideplane-server", "error", err)
			return err
		}
		if redirectServer != nil {
			if err := redirectServer.Shutdown(shutdownCtx); err != nil {
				logger.Error("shutdown sideplane-server redirector", "error", err)
				return err
			}
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("sideplane-server stopped", "error", err)
			return err
		}
		if redirectServer != nil {
			if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("sideplane-server redirector stopped", "error", err)
				return err
			}
		}
	}
	return nil
}

func newTLSRedirectServer(addr string, httpsAddr string) *http.Server {
	return &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target := *r.URL
			target.Scheme = "https"
			target.Host = redirectHost(r.Host, httpsAddr)
			http.Redirect(w, r, target.String(), http.StatusMovedPermanently)
		}),
	}
}

func redirectHost(requestHost string, httpsAddr string) string {
	httpsAddr = strings.TrimSpace(httpsAddr)
	host, port, err := net.SplitHostPort(httpsAddr)
	if err != nil {
		if strings.HasPrefix(httpsAddr, ":") {
			port = strings.TrimPrefix(httpsAddr, ":")
		} else if httpsAddr != "" {
			return httpsAddr
		}
	}
	if host != "" && host != "0.0.0.0" && host != "::" && host != "[::]" {
		return net.JoinHostPort(host, port)
	}
	requestHostname := requestHost
	if parsedHost, _, err := net.SplitHostPort(requestHost); err == nil {
		requestHostname = parsedHost
	}
	if port == "" {
		return requestHost
	}
	return net.JoinHostPort(requestHostname, port)
}

func freeTCPAddr() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	return listener.Addr().String(), nil
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

func applyBoolEnvFallback(setFlags map[string]bool, flagName string, envName string, value *bool) error {
	if value == nil || setFlags[flagName] {
		return nil
	}
	envValue, ok := os.LookupEnv(envName)
	if !ok || strings.TrimSpace(envValue) == "" {
		return nil
	}
	parsed, err := parseServerConfigBool(envName, envValue)
	if err != nil {
		return err
	}
	*value = parsed
	return nil
}

func parseServerConfigDuration(name string, value string) (time.Duration, error) {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

func parseServerConfigInt(name string, value string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

func parseServerConfigBool(name string, value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on":
		return true, nil
	case "0", "f", "false", "n", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s: expected boolean value", name)
	}
}
