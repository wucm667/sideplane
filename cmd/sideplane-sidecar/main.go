package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wucm667/sideplane/internal/buildinfo"
	"github.com/wucm667/sideplane/internal/sidecar"
	"github.com/wucm667/sideplane/pkg/adapters/hermes"
	"github.com/wucm667/sideplane/pkg/adapters/openclaw"
	"github.com/wucm667/sideplane/pkg/adapters/registry"
	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	"github.com/wucm667/sideplane/pkg/protocol"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "enroll" {
		return runEnroll(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "doctor" {
		return runDoctor(args[1:], stdout, stderr)
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
	openclawConfigPaths := flags.String("openclaw-config-paths", "", "path-list of read-only OpenClaw config files to inspect; can also be set with SIDEPLANE_OPENCLAW_CONFIG_PATHS")
	hermesDockerContainer := flags.String("hermes-docker-container", "", "optional read-only Docker container name for Hermes status/log inspection; can also be set with SIDEPLANE_HERMES_DOCKER_CONTAINER")
	hermesServiceUnit := flags.String("hermes-service-unit", "", "optional systemd unit used as the Hermes restart target when no docker container is set; can also be set with SIDEPLANE_HERMES_SERVICE_UNIT")
	openclawDockerContainer := flags.String("openclaw-docker-container", "", "optional Docker container name used as the OpenClaw restart target; can also be set with SIDEPLANE_OPENCLAW_DOCKER_CONTAINER")
	openclawServiceUnit := flags.String("openclaw-service-unit", "", "optional systemd unit used as the OpenClaw restart target when no docker container is set; can also be set with SIDEPLANE_OPENCLAW_SERVICE_UNIT")
	serverPublicKey := flags.String("server-public-key", "", "base64 ed25519 server public key for signed config plans")
	applyWorkDir := flags.String("apply-work-dir", "", "sidecar-controlled work directory for config apply dry runs")
	allowLiveApply := flags.Bool("allow-live-apply", false, "DANGEROUS: allow live config replace and restart; off by default (dry-run only)")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, buildinfo.Format("sideplane-sidecar"))
		return 0
	}

	setFlags := visitedFlags(flags)
	if err := applySidecarEnvFallbacks(setFlags, sidecarFlagValues{
		serverURL:               serverURL,
		nodeID:                  nodeID,
		statePath:               statePath,
		heartbeatInterval:       heartbeatInterval,
		jobPollInterval:         jobPollInterval,
		hermesConfigPaths:       hermesConfigPaths,
		openclawConfigPaths:     openclawConfigPaths,
		hermesDockerContainer:   hermesDockerContainer,
		hermesServiceUnit:       hermesServiceUnit,
		openclawDockerContainer: openclawDockerContainer,
		openclawServiceUnit:     openclawServiceUnit,
		serverPublicKey:         serverPublicKey,
		applyWorkDir:            applyWorkDir,
	}); err != nil {
		fmt.Fprintf(stderr, "invalid environment configuration: %v\n", err)
		return 1
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
	if value := strings.TrimSpace(*hermesServiceUnit); value != "" {
		hermesOptions = append(hermesOptions, hermes.WithServiceUnit(value))
	}
	hermesOptions = append(hermesOptions, hermes.WithAllowLiveApply(*allowLiveApply))
	hermesAdapter := hermes.NewAdapter(hermesOptions...)

	openclawOptions := []openclaw.Option{}
	if value := strings.TrimSpace(*openclawConfigPaths); value != "" {
		openclawOptions = append(openclawOptions, openclaw.WithConfigPaths(splitPathList(value)...))
	}
	if value := strings.TrimSpace(*openclawDockerContainer); value != "" {
		openclawOptions = append(openclawOptions, openclaw.WithDockerContainer(value))
	}
	if value := strings.TrimSpace(*openclawServiceUnit); value != "" {
		openclawOptions = append(openclawOptions, openclaw.WithServiceUnit(value))
	}
	openclawOptions = append(openclawOptions, openclaw.WithAllowLiveApply(*allowLiveApply))
	openclawAdapter := openclaw.NewAdapter(openclawOptions...)
	reg := registry.New(hermesAdapter, openclawAdapter)

	client, err := sidecar.NewHeartbeatClient(sidecar.HeartbeatClientConfig{
		ServerURL:      runtimeConfig.ServerURL,
		NodeID:         runtimeConfig.NodeID,
		NodeCredential: runtimeConfig.NodeCredential,
		SidecarVersion: buildinfo.Version,
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
		Controller:     hermesAdapter,
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

type doctorReport struct {
	ServerURL               string               `json:"serverUrl,omitempty"`
	StatePath               string               `json:"statePath"`
	StateFound              bool                 `json:"stateFound"`
	NodeID                  string               `json:"nodeId,omitempty"`
	HermesConfigPaths       []sidecar.PathStatus `json:"hermesConfigPaths"`
	OpenClawConfigPaths     []sidecar.PathStatus `json:"openclawConfigPaths"`
	ApplyWorkDir            string               `json:"applyWorkDir"`
	LiveApplyEnabled        bool                 `json:"liveApplyEnabled"`
	PublicKeyStatus         string               `json:"publicKeyStatus"`
	HermesDockerContainer   string               `json:"hermesDockerContainer,omitempty"`
	HermesServiceUnit       string               `json:"hermesServiceUnit,omitempty"`
	OpenClawDockerContainer string               `json:"openclawDockerContainer,omitempty"`
	OpenClawServiceUnit     string               `json:"openclawServiceUnit,omitempty"`
}

func runDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane-sidecar doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL")
	nodeID := flags.String("node-id", "", "node ID override")
	statePath := flags.String("state", "", "sidecar state file path")
	heartbeatInterval := flags.Duration("heartbeat-interval", 30*time.Second, "heartbeat interval")
	jobPollInterval := flags.Duration("job-poll-interval", 30*time.Second, "job poll interval")
	hermesConfigPaths := flags.String("hermes-config-paths", "", "path-list of read-only Hermes config files to inspect; can also be set with SIDEPLANE_HERMES_CONFIG_PATHS")
	openclawConfigPaths := flags.String("openclaw-config-paths", "", "path-list of read-only OpenClaw config files to inspect; can also be set with SIDEPLANE_OPENCLAW_CONFIG_PATHS")
	hermesDockerContainer := flags.String("hermes-docker-container", "", "optional read-only Docker container name for Hermes status/log inspection; can also be set with SIDEPLANE_HERMES_DOCKER_CONTAINER")
	hermesServiceUnit := flags.String("hermes-service-unit", "", "optional systemd unit used as the Hermes restart target when no docker container is set; can also be set with SIDEPLANE_HERMES_SERVICE_UNIT")
	openclawDockerContainer := flags.String("openclaw-docker-container", "", "optional Docker container name used as the OpenClaw restart target; can also be set with SIDEPLANE_OPENCLAW_DOCKER_CONTAINER")
	openclawServiceUnit := flags.String("openclaw-service-unit", "", "optional systemd unit used as the OpenClaw restart target when no docker container is set; can also be set with SIDEPLANE_OPENCLAW_SERVICE_UNIT")
	serverPublicKey := flags.String("server-public-key", "", "base64 ed25519 server public key for signed config plans")
	applyWorkDir := flags.String("apply-work-dir", "", "sidecar-controlled work directory for config apply dry runs")
	allowLiveApply := flags.Bool("allow-live-apply", false, "report live config apply as enabled")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: sideplane-sidecar doctor [flags]")
		return 1
	}

	setFlags := visitedFlags(flags)
	if err := applySidecarEnvFallbacks(setFlags, sidecarFlagValues{
		serverURL:               serverURL,
		nodeID:                  nodeID,
		statePath:               statePath,
		heartbeatInterval:       heartbeatInterval,
		jobPollInterval:         jobPollInterval,
		hermesConfigPaths:       hermesConfigPaths,
		openclawConfigPaths:     openclawConfigPaths,
		hermesDockerContainer:   hermesDockerContainer,
		hermesServiceUnit:       hermesServiceUnit,
		openclawDockerContainer: openclawDockerContainer,
		openclawServiceUnit:     openclawServiceUnit,
		serverPublicKey:         serverPublicKey,
		applyWorkDir:            applyWorkDir,
	}); err != nil {
		fmt.Fprintf(stderr, "invalid environment configuration: %v\n", err)
		return 1
	}

	cfg, err := resolveRuntimeConfig(*serverURL, *nodeID, "", *statePath)
	if err != nil {
		fmt.Fprintf(stderr, "load sidecar state: %v\n", err)
		return 1
	}
	stateFound := false
	if _, err := os.Stat(cfg.StatePath); err == nil {
		stateFound = true
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(stderr, "stat sidecar state: %v\n", err)
		return 1
	}

	workDir := strings.TrimSpace(*applyWorkDir)
	if workDir == "" {
		workDir = filepath.Join(os.TempDir(), "sideplane-apply")
	}
	report := doctorReport{
		ServerURL:               cfg.ServerURL,
		StatePath:               cfg.StatePath,
		StateFound:              stateFound,
		NodeID:                  cfg.NodeID,
		HermesConfigPaths:       sidecar.CheckReadablePaths(splitPathList(*hermesConfigPaths)),
		OpenClawConfigPaths:     sidecar.CheckReadablePaths(splitPathList(*openclawConfigPaths)),
		ApplyWorkDir:            workDir,
		LiveApplyEnabled:        *allowLiveApply,
		PublicKeyStatus:         publicKeyStatus(*serverPublicKey),
		HermesDockerContainer:   strings.TrimSpace(*hermesDockerContainer),
		HermesServiceUnit:       strings.TrimSpace(*hermesServiceUnit),
		OpenClawDockerContainer: strings.TrimSpace(*openclawDockerContainer),
		OpenClawServiceUnit:     strings.TrimSpace(*openclawServiceUnit),
	}

	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "encode doctor report: %v\n", err)
			return 1
		}
		return 0
	}
	printDoctorReport(stdout, report)
	return 0
}

func publicKeyStatus(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "missing"
	}
	if _, err := spcrypto.ParsePublicKey(raw); err != nil {
		return "invalid"
	}
	return "valid"
}

func printDoctorReport(w io.Writer, report doctorReport) {
	fmt.Fprintf(w, "Server URL: %s\n", valueOrDash(report.ServerURL))
	fmt.Fprintf(w, "State path: %s\n", report.StatePath)
	fmt.Fprintf(w, "State found: %s\n", yesNo(report.StateFound))
	fmt.Fprintf(w, "Node ID: %s\n", valueOrDash(report.NodeID))
	fmt.Fprintf(w, "Apply work dir: %s\n", report.ApplyWorkDir)
	fmt.Fprintf(w, "Live apply: %s\n", yesNo(report.LiveApplyEnabled))
	fmt.Fprintf(w, "Public key: %s\n", report.PublicKeyStatus)
	fmt.Fprintf(w, "Hermes docker container: %s\n", valueOrDash(report.HermesDockerContainer))
	fmt.Fprintf(w, "Hermes service unit: %s\n", valueOrDash(report.HermesServiceUnit))
	fmt.Fprintf(w, "OpenClaw docker container: %s\n", valueOrDash(report.OpenClawDockerContainer))
	fmt.Fprintf(w, "OpenClaw service unit: %s\n", valueOrDash(report.OpenClawServiceUnit))
	printPathStatuses(w, "Hermes config paths", report.HermesConfigPaths)
	printPathStatuses(w, "OpenClaw config paths", report.OpenClawConfigPaths)
}

func printPathStatuses(w io.Writer, label string, statuses []sidecar.PathStatus) {
	fmt.Fprintf(w, "%s:\n", label)
	if len(statuses) == 0 {
		fmt.Fprintln(w, "  (none configured)")
		return
	}
	for _, status := range statuses {
		state := "readable"
		if !status.Readable {
			state = "unreadable"
		}
		if status.Error != "" {
			fmt.Fprintf(w, "  %s %s (%s)\n", status.Path, state, status.Error)
			continue
		}
		fmt.Fprintf(w, "  %s %s\n", status.Path, state)
	}
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

type sidecarFlagValues struct {
	serverURL               *string
	nodeID                  *string
	statePath               *string
	heartbeatInterval       *time.Duration
	jobPollInterval         *time.Duration
	hermesConfigPaths       *string
	openclawConfigPaths     *string
	hermesDockerContainer   *string
	hermesServiceUnit       *string
	openclawDockerContainer *string
	openclawServiceUnit     *string
	serverPublicKey         *string
	applyWorkDir            *string
}

func visitedFlags(flags *flag.FlagSet) map[string]bool {
	visited := map[string]bool{}
	flags.Visit(func(f *flag.Flag) {
		visited[f.Name] = true
	})
	return visited
}

func applySidecarEnvFallbacks(setFlags map[string]bool, values sidecarFlagValues) error {
	applyStringEnvFallback(setFlags, "server", "SIDEPLANE_SERVER_URL", values.serverURL)
	applyStringEnvFallback(setFlags, "node-id", "SIDEPLANE_NODE_ID", values.nodeID)
	applyStringEnvFallback(setFlags, "state", "SIDEPLANE_SIDECAR_STATE", values.statePath)
	applyStringEnvFallback(setFlags, "hermes-config-paths", "SIDEPLANE_HERMES_CONFIG_PATHS", values.hermesConfigPaths)
	applyStringEnvFallback(setFlags, "openclaw-config-paths", "SIDEPLANE_OPENCLAW_CONFIG_PATHS", values.openclawConfigPaths)
	applyStringEnvFallback(setFlags, "hermes-docker-container", "SIDEPLANE_HERMES_DOCKER_CONTAINER", values.hermesDockerContainer)
	applyStringEnvFallback(setFlags, "hermes-service-unit", "SIDEPLANE_HERMES_SERVICE_UNIT", values.hermesServiceUnit)
	applyStringEnvFallback(setFlags, "openclaw-docker-container", "SIDEPLANE_OPENCLAW_DOCKER_CONTAINER", values.openclawDockerContainer)
	applyStringEnvFallback(setFlags, "openclaw-service-unit", "SIDEPLANE_OPENCLAW_SERVICE_UNIT", values.openclawServiceUnit)
	applyStringEnvFallback(setFlags, "server-public-key", "SIDEPLANE_SERVER_PUBLIC_KEY", values.serverPublicKey)
	applyStringEnvFallback(setFlags, "apply-work-dir", "SIDEPLANE_APPLY_WORK_DIR", values.applyWorkDir)
	if err := applyDurationEnvFallback(setFlags, "heartbeat-interval", "SIDEPLANE_HEARTBEAT_INTERVAL", values.heartbeatInterval); err != nil {
		return err
	}
	if err := applyDurationEnvFallback(setFlags, "job-poll-interval", "SIDEPLANE_JOB_POLL_INTERVAL", values.jobPollInterval); err != nil {
		return err
	}
	return nil
}

func applyStringEnvFallback(setFlags map[string]bool, flagName string, envName string, value *string) {
	if value == nil || setFlags[flagName] || strings.TrimSpace(*value) != "" {
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
		SidecarVersion: buildinfo.Version,
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
