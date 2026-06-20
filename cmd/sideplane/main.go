package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wucm667/sideplane/internal/auth"
	"github.com/wucm667/sideplane/internal/buildinfo"
	spconfig "github.com/wucm667/sideplane/pkg/config"
	"github.com/wucm667/sideplane/pkg/protocol"
)

const serverURLEnv = "SIDEPLANE_SERVER_URL"

var cliStdin io.Reader = os.Stdin

type cliNodeStatus struct {
	protocol.NodeStatus
	Drift bool `json:"drift"`
}

type cliListNodesResponse struct {
	Nodes  []cliNodeStatus `json:"nodes"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || isHelpArg(args[0]) {
		printHelp(stdout)
		return 0
	}
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Fprintln(stdout, buildinfo.Format("sideplane"))
		return 0
	}

	switch args[0] {
	case "fleet":
		if len(args) >= 2 && args[1] == "status" {
			return runFleetStatus(args[2:], stdout, stderr)
		}
	case "probe":
		return runProbe(args[1:], stdout, stderr)
	case "restart":
		return runRestart(args[1:], stdout, stderr)
	case "rollback":
		return runRollback(args[1:], stdout, stderr)
	case "backups":
		if len(args) >= 2 && args[1] == "list" {
			return runBackupsList(args[2:], stdout, stderr)
		}
	case "rollout":
		if len(args) >= 2 {
			switch args[1] {
			case "create":
				return runRolloutCreate(args[2:], stdout, stderr)
			case "list":
				return runRolloutList(args[2:], stdout, stderr)
			case "status":
				return runRolloutStatus(args[2:], stdout, stderr)
			case "pause":
				return runRolloutAction(args[2:], stdout, stderr, protocol.RolloutActionPause)
			case "resume":
				return runRolloutAction(args[2:], stdout, stderr, protocol.RolloutActionResume)
			case "abort":
				return runRolloutAction(args[2:], stdout, stderr, protocol.RolloutActionAbort)
			}
		}
	case "jobs":
		if len(args) >= 2 && args[1] == "list" {
			return runJobsList(args[2:], stdout, stderr)
		}
	case "audit":
		if len(args) >= 2 && args[1] == "list" {
			return runAuditList(args[2:], stdout, stderr)
		}
	case "token":
		if len(args) >= 2 {
			switch args[1] {
			case "create":
				return runTokenCreate(args[2:], stdout, stderr)
			case "list":
				return runTokenList(args[2:], stdout, stderr)
			case "revoke":
				return runTokenRevoke(args[2:], stdout, stderr)
			}
		}
	case "config":
		if len(args) >= 2 && args[1] == "apply" {
			return runConfigApply(args[2:], stdout, stderr)
		}
		if len(args) >= 2 && args[1] == "preview" {
			return runConfigPreview(args[2:], stdout, stderr)
		}
		if len(args) >= 2 && args[1] == "get" {
			return runConfigGet(args[2:], stdout, stderr)
		}
		if len(args) >= 2 && args[1] == "set" {
			return runConfigSet(args[2:], stdout, stderr)
		}
		if len(args) >= 2 && args[1] == "history" {
			return runConfigHistory(args[2:], stdout, stderr)
		}
		if len(args) >= 2 && args[1] == "revert" {
			return runConfigRevert(args[2:], stdout, stderr)
		}
	case "node":
		if len(args) >= 2 && args[1] == "inspect" {
			return runNodeInspect(args[2:], stdout, stderr)
		}
		if len(args) >= 2 && args[1] == "label" {
			return runNodeLabel(args[2:], stdout, stderr)
		}
		if len(args) >= 2 && args[1] == "remove" {
			return runNodeRemove(args[2:], stdout, stderr)
		}
	case "enrollment":
		if len(args) >= 2 && args[1] == "create" {
			return runEnrollmentCreate(args[2:], stdout, stderr)
		}
	}

	fmt.Fprintf(stderr, "unknown command: %s\n\n", strings.Join(args, " "))
	printHelp(stderr)
	return 1
}

func isHelpArg(arg string) bool {
	return arg == "--help" || arg == "-h" || arg == "help"
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `Usage: sideplane <command>

Commands:
  fleet status        Show fleet node status
  probe <nodeId>      Run a deep probe on a node
  restart <nodeId>    Create a standalone restart job
  rollback <nodeId>   Create a rollback job from a backup ref
  backups list <id>   List rollback backups for a node
  rollout create      Create a staged fleet rollout
  rollout list        List fleet rollouts
  rollout status <id> Show rollout batch and node progress
  jobs list <nodeId>  List node jobs
  audit list          List audit events
  token create        Create a named operator token
  token list          List named operator token metadata
  token revoke <id>   Revoke a named operator token
  config apply <id>   Create a config apply job
  config preview <id> Preview effective node configuration
  config get          Show desired configuration
  config set          Update global desired configuration
  config history      List desired configuration history
  config revert <id>  Revert desired configuration
  node inspect <id>   Show full node detail
  node label <id>     Set or remove node labels
  node remove <id>    Remove a node from the fleet
  enrollment create   Create a one-time enrollment token
  version             Print version
`)
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

func runRolloutCreate(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane rollout create", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	selector := flags.String("selector", "", "label selector with AND semantics, for example role=canary,zone=lab")
	var nodeIDs stringList
	flags.Var(&nodeIDs, "node", "target node ID; may be repeated")
	provider := flags.String("provider", "", "target provider")
	model := flags.String("model", "", "target model")
	runtimeType := flags.String("runtime-type", "hermes", "runtime type")
	profile := flags.String("profile", "default", "runtime profile")
	batchSize := flags.Int("batch-size", 1, "sequential rollout batch size")
	live := flags.Bool("live", false, "request live config apply instead of dry-run")
	yes := flags.Bool("yes", false, "confirm live rollout")
	healthTimeout := flags.Duration("health-timeout", 0, "batch health timeout; server default is used when omitted")
	jsonOutput := flags.Bool("json", false, "print raw JSON response")
	usage := "sideplane rollout create (--selector key=value[,key2=value2] | --node NODE [--node NODE...]) --provider PROVIDER --model MODEL [--runtime-type TYPE] [--profile PROFILE] [--batch-size N] [--live --yes] [--health-timeout DURATION] [--server URL] [--operator-token TOKEN] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "live", "yes", "json"); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "sideplane rollout create: unexpected positional arguments")
		return 1
	}
	if *live && !*yes {
		fmt.Fprintln(stderr, "rollout create: --live requires --yes")
		return 1
	}
	if strings.TrimSpace(*provider) == "" || strings.TrimSpace(*model) == "" {
		fmt.Fprintln(stderr, "rollout create: --provider and --model are required")
		return 1
	}
	if *batchSize <= 0 {
		fmt.Fprintln(stderr, "rollout create: --batch-size must be positive")
		return 1
	}
	if *healthTimeout < 0 {
		fmt.Fprintln(stderr, "rollout create: --health-timeout must be zero or positive")
		return 1
	}
	selectorMap, err := parseCLISelector(*selector)
	if err != nil {
		fmt.Fprintf(stderr, "rollout create: %v\n", err)
		return 1
	}
	trimmedNodes := uniqueTrimmedCLIStrings(nodeIDs)
	if len(selectorMap) == 0 && len(trimmedNodes) == 0 {
		fmt.Fprintln(stderr, "rollout create: --selector or --node is required")
		return 1
	}
	if len(selectorMap) > 0 && len(trimmedNodes) > 0 {
		fmt.Fprintln(stderr, "rollout create: --selector and --node are mutually exclusive")
		return 1
	}

	req := protocol.CreateRolloutRequest{Spec: protocol.RolloutSpec{
		Selector:      selectorMap,
		NodeIDs:       trimmedNodes,
		RuntimeType:   strings.TrimSpace(*runtimeType),
		Profile:       strings.TrimSpace(*profile),
		Target:        protocol.ProviderModelConfig{Provider: strings.TrimSpace(*provider), Model: strings.TrimSpace(*model)},
		BatchSize:     *batchSize,
		Live:          *live,
		HealthTimeout: *healthTimeout,
	}}
	resp, body, err := postJSON[protocol.CreateRolloutResponse](context.Background(), serverURLValue(*serverURL), "/api/rollouts", req, operatorTokenValue(*operatorTokenFlag))
	if err != nil {
		fmt.Fprintf(stderr, "rollout create: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	printRolloutSummary(stdout, resp.Rollout)
	return 0
}

func runRolloutList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane rollout list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	jsonOutput := flags.Bool("json", false, "print raw JSON response")
	usage := "sideplane rollout list [--server URL] [--operator-token TOKEN] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json"); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "sideplane rollout list: unexpected positional arguments")
		return 1
	}

	resp, body, err := getJSON[protocol.ListRolloutsResponse](context.Background(), serverURLValue(*serverURL), "/api/rollouts", operatorTokenValue(*operatorTokenFlag))
	if err != nil {
		fmt.Fprintf(stderr, "rollout list: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	printRolloutsTable(stdout, resp.Rollouts)
	return 0
}

func runRolloutStatus(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane rollout status", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	watch := flags.Bool("watch", false, "poll until the rollout reaches a terminal state")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	usage := "sideplane rollout status <id> [--server URL] [--operator-token TOKEN] [--watch] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "watch", "json"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	rolloutID := strings.TrimSpace(flags.Arg(0))
	if rolloutID == "" {
		fmt.Fprintln(stderr, "rollout status: id is required")
		return 1
	}

	server := serverURLValue(*serverURL)
	operatorToken := operatorTokenValue(*operatorTokenFlag)
	if *watch {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		rollout, err := waitForRollout(ctx, server, rolloutID, operatorToken)
		if err != nil {
			fmt.Fprintf(stderr, "rollout status: %v\n", err)
			return 1
		}
		if *jsonOutput {
			writeJSONValue(stdout, protocol.GetRolloutResponse{Rollout: rollout})
			return 0
		}
		printRolloutDetail(stdout, rollout)
		return 0
	}

	resp, body, err := getJSON[protocol.GetRolloutResponse](context.Background(), server, rolloutPath(rolloutID), operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "rollout status: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	printRolloutDetail(stdout, resp.Rollout)
	return 0
}

func runRolloutAction(args []string, stdout io.Writer, stderr io.Writer, action protocol.RolloutAction) int {
	command := "sideplane rollout " + string(action)
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	jsonOutput := flags.Bool("json", false, "print raw JSON response")
	usage := command + " <id> [--server URL] [--operator-token TOKEN] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	rolloutID := strings.TrimSpace(flags.Arg(0))
	if rolloutID == "" {
		fmt.Fprintf(stderr, "rollout %s: id is required\n", action)
		return 1
	}

	resp, body, err := postJSON[protocol.RolloutActionResponse](
		context.Background(),
		serverURLValue(*serverURL),
		rolloutPath(rolloutID)+"/actions",
		protocol.RolloutActionRequest{Action: action},
		operatorTokenValue(*operatorTokenFlag),
	)
	if err != nil {
		fmt.Fprintf(stderr, "rollout %s: %v\n", action, err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	printRolloutSummary(stdout, resp.Rollout)
	return 0
}

func runRollback(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane rollback", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	runtimeType := flags.String("runtime-type", "hermes", "runtime type")
	profile := flags.String("profile", "default", "runtime profile")
	backupRef := flags.String("backup-ref", "", "rollback backup reference")
	live := flags.Bool("live", false, "request live rollback instead of dry-run")
	yes := flags.Bool("yes", false, "confirm live rollback")
	wait := flags.Bool("wait", false, "poll until the rollback job completes or fails")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	usage := "sideplane rollback <nodeId> [--server URL] [--operator-token TOKEN] [--backup-ref REF] [--runtime-type TYPE] [--profile PROFILE] [--live --yes] [--wait] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "live", "yes", "wait", "json"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	if *live && !*yes {
		fmt.Fprintln(stderr, "rollback: --live requires --yes")
		return 1
	}
	nodeID := strings.TrimSpace(flags.Arg(0))
	if nodeID == "" {
		fmt.Fprintln(stderr, "rollback: nodeId is required")
		return 1
	}

	server := serverURLValue(*serverURL)
	operatorToken := operatorTokenValue(*operatorTokenFlag)
	backupRefValue := strings.TrimSpace(*backupRef)
	if backupRefValue == "" {
		latest, err := latestNodeBackup(context.Background(), server, nodeID, operatorToken)
		if err != nil {
			fmt.Fprintf(stderr, "rollback: %v\n", err)
			return 1
		}
		backupRefValue = latest.Ref
	}
	path := "/api/nodes/" + url.PathEscape(nodeID) + "/rollback"
	job, body, err := postJSON[protocol.Job](context.Background(), server, path, protocol.RollbackRequest{
		RuntimeType: strings.TrimSpace(*runtimeType),
		Profile:     strings.TrimSpace(*profile),
		BackupRef:   backupRefValue,
		Live:        *live,
	}, operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "rollback: %v\n", err)
		return 1
	}

	if !*wait {
		if *jsonOutput {
			writeRawJSON(stdout, body)
			return 0
		}
		printRollbackJobSummary(stdout, job)
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	finalJob, err := waitForNodeJob(ctx, server, nodeID, job.ID, operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "rollback wait: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeJSONValue(stdout, finalJob)
		return 0
	}
	printRollbackJobSummary(stdout, finalJob)
	printRollbackResultSummary(stdout, finalJob)
	return 0
}

func runBackupsList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane backups list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	limit := flags.Int("limit", 0, "maximum backups to list")
	jsonOutput := flags.Bool("json", false, "print raw JSON response")
	usage := "sideplane backups list <nodeId> [--server URL] [--operator-token TOKEN] [--limit N] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	if *limit < 0 {
		fmt.Fprintln(stderr, "backups list: --limit must be positive")
		return 1
	}
	nodeID := strings.TrimSpace(flags.Arg(0))
	if nodeID == "" {
		fmt.Fprintln(stderr, "backups list: nodeId is required")
		return 1
	}

	path := nodeBackupsPath(nodeID, *limit)
	resp, body, err := getJSON[protocol.ListRollbackBackupsResponse](context.Background(), serverURLValue(*serverURL), path, operatorTokenValue(*operatorTokenFlag))
	if err != nil {
		fmt.Fprintf(stderr, "backups list: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	printBackupsTable(stdout, resp.Backups)
	return 0
}

func runRestart(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane restart", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	runtimeType := flags.String("runtime-type", "hermes", "runtime type")
	profile := flags.String("profile", "default", "runtime profile")
	live := flags.Bool("live", false, "request live restart instead of dry-run")
	yes := flags.Bool("yes", false, "confirm live restart")
	wait := flags.Bool("wait", false, "poll until the restart job completes or fails")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	usage := "sideplane restart <nodeId> [--server URL] [--operator-token TOKEN] [--runtime-type TYPE] [--profile PROFILE] [--live --yes] [--wait] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "live", "yes", "wait", "json"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	if *live && !*yes {
		fmt.Fprintln(stderr, "restart: --live requires --yes")
		return 1
	}
	nodeID := strings.TrimSpace(flags.Arg(0))
	if nodeID == "" {
		fmt.Fprintln(stderr, "restart: nodeId is required")
		return 1
	}

	server := serverURLValue(*serverURL)
	operatorToken := operatorTokenValue(*operatorTokenFlag)
	path := "/api/nodes/" + url.PathEscape(nodeID) + "/restart"
	job, body, err := postJSON[protocol.Job](context.Background(), server, path, protocol.RestartRequest{
		RuntimeType: strings.TrimSpace(*runtimeType),
		Profile:     strings.TrimSpace(*profile),
		Live:        *live,
	}, operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "restart: %v\n", err)
		return 1
	}

	if !*wait {
		if *jsonOutput {
			writeRawJSON(stdout, body)
			return 0
		}
		printRestartJobSummary(stdout, job)
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	finalJob, err := waitForNodeJob(ctx, server, nodeID, job.ID, operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "restart wait: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeJSONValue(stdout, finalJob)
		return 0
	}
	printRestartJobSummary(stdout, finalJob)
	printRestartResultSummary(stdout, finalJob)
	return 0
}

func runConfigApply(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane config apply", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	runtimeType := flags.String("runtime-type", "hermes", "runtime type")
	profile := flags.String("profile", "default", "runtime profile")
	configPath := flags.String("config-path", "", "operator reference for expected config path; server uses last deep-probe path")
	live := flags.Bool("live", false, "request live apply instead of dry-run")
	yes := flags.Bool("yes", false, "confirm live apply")
	wait := flags.Bool("wait", false, "poll until the config apply job completes or fails")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	usage := "sideplane config apply <nodeId> [--server URL] [--operator-token TOKEN] [--runtime-type TYPE] [--profile PROFILE] [--config-path PATH] [--live --yes] [--wait] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "live", "yes", "wait", "json"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	if *live && !*yes {
		fmt.Fprintln(stderr, "config apply: --live requires --yes")
		return 1
	}
	nodeID := strings.TrimSpace(flags.Arg(0))
	if nodeID == "" {
		fmt.Fprintln(stderr, "config apply: nodeId is required")
		return 1
	}

	dryRun := !*live
	server := serverURLValue(*serverURL)
	operatorToken := operatorTokenValue(*operatorTokenFlag)
	path := "/api/nodes/" + url.PathEscape(nodeID) + "/config-apply"
	job, body, err := postJSON[protocol.Job](context.Background(), server, path, protocol.ConfigApplyRequest{
		RuntimeType: strings.TrimSpace(*runtimeType),
		Profile:     strings.TrimSpace(*profile),
		DryRun:      &dryRun,
	}, operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "config apply: %v\n", err)
		return 1
	}

	if !*wait {
		if *jsonOutput {
			writeRawJSON(stdout, body)
			return 0
		}
		printConfigApplyJobSummary(stdout, job, strings.TrimSpace(*configPath))
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	finalJob, err := waitForNodeJob(ctx, server, nodeID, job.ID, operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "config apply wait: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeJSONValue(stdout, finalJob)
		return 0
	}
	printConfigApplyJobSummary(stdout, finalJob, strings.TrimSpace(*configPath))
	printConfigApplyResultSummary(stdout, finalJob)
	return 0
}

func runConfigPreview(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane config preview", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	runtimeType := flags.String("runtime-type", "hermes", "runtime type")
	profile := flags.String("profile", "default", "runtime profile")
	actualHash := flags.String("actual-hash", "", "optional actual config hash to display")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	usage := "sideplane config preview <nodeId> [--server URL] [--runtime-type TYPE] [--profile PROFILE] [--actual-hash HASH] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	nodeID := strings.TrimSpace(flags.Arg(0))
	if nodeID == "" {
		fmt.Fprintln(stderr, "config preview: nodeId is required")
		return 1
	}

	params := url.Values{}
	params.Set("nodeId", nodeID)
	if trimmed := strings.TrimSpace(*runtimeType); trimmed != "" {
		params.Set("runtimeType", trimmed)
	}
	if trimmed := strings.TrimSpace(*profile); trimmed != "" {
		params.Set("profile", trimmed)
	}
	if trimmed := strings.TrimSpace(*actualHash); trimmed != "" {
		params.Set("actualHash", trimmed)
	}
	path := "/api/config/effective?" + params.Encode()
	effective, body, err := getJSON[protocol.EffectiveConfigResponse](context.Background(), serverURLValue(*serverURL), path, "")
	if err != nil {
		fmt.Fprintf(stderr, "config preview: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	printEffectiveConfigPreview(stdout, effective, strings.TrimSpace(*actualHash))
	return 0
}

func runAuditList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane audit list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	nodeID := flags.String("node-id", "", "optional node ID filter")
	action := flags.String("action", "", "optional audit action filter")
	limit := flags.Int("limit", 0, "maximum audit events to list")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	usage := "sideplane audit list [--server URL] [--node-id NODE] [--action ACTION] [--limit N] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json"); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	if *limit < 0 {
		fmt.Fprintln(stderr, "audit list: --limit must be positive")
		return 1
	}

	params := url.Values{}
	if trimmed := strings.TrimSpace(*nodeID); trimmed != "" {
		params.Set("nodeId", trimmed)
	}
	if trimmed := strings.TrimSpace(*action); trimmed != "" {
		params.Set("action", trimmed)
	}
	if *limit > 0 {
		params.Set("limit", strconv.Itoa(*limit))
	}
	path := "/api/audit"
	if query := params.Encode(); query != "" {
		path += "?" + query
	}
	resp, body, err := getJSON[protocol.ListAuditEventsResponse](context.Background(), serverURLValue(*serverURL), path, "")
	if err != nil {
		fmt.Fprintf(stderr, "audit list: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	printAuditTable(stdout, resp.Events)
	return 0
}

func runJobsList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane jobs list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	limit := flags.Int("limit", 0, "maximum jobs to list")
	status := flags.String("status", "", "optional job status filter: pending, claimed, completed, failed")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	usage := "sideplane jobs list <nodeId> [--server URL] [--operator-token TOKEN] [--limit N] [--status STATUS] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	if *limit < 0 {
		fmt.Fprintln(stderr, "jobs list: --limit must be positive")
		return 1
	}
	statusValue := strings.TrimSpace(*status)
	if statusValue != "" && !validCLIJobStatus(protocol.JobStatus(statusValue)) {
		fmt.Fprintf(stderr, "jobs list: unsupported status %q\n", statusValue)
		return 1
	}

	nodeID := strings.TrimSpace(flags.Arg(0))
	if nodeID == "" {
		fmt.Fprintln(stderr, "jobs list: nodeId is required")
		return 1
	}

	params := url.Values{}
	if *limit > 0 {
		params.Set("limit", strconv.Itoa(*limit))
	}
	if statusValue != "" {
		params.Set("status", statusValue)
	}
	path := "/api/nodes/" + url.PathEscape(nodeID) + "/jobs"
	if query := params.Encode(); query != "" {
		path += "?" + query
	}
	jobs, body, err := getJSON[[]protocol.Job](context.Background(), serverURLValue(*serverURL), path, operatorTokenValue(*operatorTokenFlag))
	if err != nil {
		fmt.Fprintf(stderr, "jobs list: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	printJobsTable(stdout, jobs)
	return 0
}

func runNodeInspect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane node inspect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	usage := "sideplane node inspect <nodeId> [--server URL] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}

	nodeID := strings.TrimSpace(flags.Arg(0))
	if nodeID == "" {
		fmt.Fprintln(stderr, "node inspect: nodeId is required")
		return 1
	}

	nodes, _, err := getNodeList(context.Background(), serverURLValue(*serverURL), "")
	if err != nil {
		fmt.Fprintf(stderr, "node inspect: %v\n", err)
		return 1
	}
	for _, node := range nodes {
		if node.NodeID != nodeID {
			continue
		}
		if *jsonOutput {
			writeJSONValue(stdout, node)
			return 0
		}
		printNodeInspect(stdout, node)
		return 0
	}

	fmt.Fprintf(stderr, "node inspect: node %q not found\n", nodeID)
	return 1
}

func runNodeLabel(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane node label", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	var removeLabels stringList
	flags.Var(&removeLabels, "remove", "label key to remove; may be repeated")
	jsonOutput := flags.Bool("json", false, "print raw JSON response")
	usage := "sideplane node label <nodeId> key=value [key2=value2 ...] [--remove key] [--server URL] [--operator-token TOKEN] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json"); err != nil {
		return 2
	}
	if flags.NArg() < 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	nodeID := strings.TrimSpace(flags.Arg(0))
	if nodeID == "" {
		fmt.Fprintln(stderr, "node label: nodeId is required")
		return 1
	}
	if flags.NArg() == 1 && len(removeLabels) == 0 {
		fmt.Fprintln(stderr, "node label: provide key=value or --remove key")
		return 1
	}

	labels := map[string]string{}
	server := serverURLValue(*serverURL)
	operatorToken := operatorTokenValue(*operatorTokenFlag)
	path := "/api/nodes/" + url.PathEscape(nodeID) + "/labels"
	current, _, err := getJSON[protocol.NodeLabelsResponse](context.Background(), server, path, operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "node label: %v\n", err)
		return 1
	}
	for key, value := range current.Labels {
		labels[key] = value
	}
	for _, key := range removeLabels {
		key = strings.TrimSpace(key)
		if key == "" {
			fmt.Fprintln(stderr, "node label: --remove key is required")
			return 1
		}
		delete(labels, key)
	}
	for _, assignment := range flags.Args()[1:] {
		key, value, ok := strings.Cut(assignment, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			fmt.Fprintf(stderr, "node label: invalid label %q, want key=value\n", assignment)
			return 1
		}
		labels[key] = strings.TrimSpace(value)
	}

	updated, body, err := putJSON[protocol.NodeLabelsResponse](context.Background(), server, path, protocol.NodeLabelsRequest{Labels: labels}, operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "node label: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	printNodeLabels(stdout, updated)
	return 0
}

func runNodeRemove(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane node remove", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	yes := flags.Bool("yes", false, "skip confirmation")
	usage := "sideplane node remove <nodeId> [--server URL] [--operator-token TOKEN] [--yes]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "yes"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}

	nodeID := strings.TrimSpace(flags.Arg(0))
	if nodeID == "" {
		fmt.Fprintln(stderr, "node remove: nodeId is required")
		return 1
	}
	if !*yes {
		confirmed, err := confirmNodeRemoval(stdout, cliStdin, nodeID)
		if err != nil {
			fmt.Fprintf(stderr, "node remove: read confirmation: %v\n", err)
			return 1
		}
		if !confirmed {
			fmt.Fprintln(stdout, "Node removal cancelled.")
			return 0
		}
	}

	path := "/api/nodes/" + url.PathEscape(nodeID)
	if _, err := apiJSONRequest(context.Background(), http.MethodDelete, serverURLValue(*serverURL), path, nil, operatorTokenValue(*operatorTokenFlag)); err != nil {
		fmt.Fprintf(stderr, "node remove: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Node %s removed.\n", nodeID)
	return 0
}

func runTokenCreate(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane token create", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	name := flags.String("name", "", "operator-visible token name")
	jsonOutput := flags.Bool("json", false, "print raw JSON response")
	usage := "sideplane token create --name NAME [--server URL] [--operator-token TOKEN] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json"); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	if strings.TrimSpace(*name) == "" {
		fmt.Fprintln(stderr, "token create: --name is required")
		return 1
	}

	resp, body, err := postJSON[protocol.CreateOperatorTokenResponse](context.Background(), serverURLValue(*serverURL), "/api/operator-tokens", protocol.CreateOperatorTokenRequest{Name: strings.TrimSpace(*name)}, operatorTokenValue(*operatorTokenFlag))
	if err != nil {
		fmt.Fprintf(stderr, "token create: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	printOperatorTokenCreated(stdout, resp)
	return 0
}

func runTokenList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane token list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	jsonOutput := flags.Bool("json", false, "print raw JSON response")
	usage := "sideplane token list [--server URL] [--operator-token TOKEN] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json"); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}

	resp, body, err := getJSON[protocol.ListOperatorTokensResponse](context.Background(), serverURLValue(*serverURL), "/api/operator-tokens", operatorTokenValue(*operatorTokenFlag))
	if err != nil {
		fmt.Fprintf(stderr, "token list: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	printOperatorTokensTable(stdout, resp.Tokens)
	return 0
}

func runTokenRevoke(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane token revoke", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	jsonOutput := flags.Bool("json", false, "print raw JSON response")
	usage := "sideplane token revoke <id> [--server URL] [--operator-token TOKEN] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	tokenID := strings.TrimSpace(flags.Arg(0))
	if tokenID == "" {
		fmt.Fprintln(stderr, "token revoke: id is required")
		return 1
	}

	body, err := apiJSONRequest(context.Background(), http.MethodDelete, serverURLValue(*serverURL), "/api/operator-tokens/"+url.PathEscape(tokenID), nil, operatorTokenValue(*operatorTokenFlag))
	if err != nil {
		fmt.Fprintf(stderr, "token revoke: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	var resp protocol.RevokeOperatorTokenResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		fmt.Fprintf(stderr, "token revoke: decode response JSON: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Operator token %s revoked.\n", resp.OperatorToken.ID)
	return 0
}

func confirmNodeRemoval(stdout io.Writer, stdin io.Reader, nodeID string) (bool, error) {
	fmt.Fprintf(stdout, "Remove node %q? [y/N] ", nodeID)
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.TrimSpace(line)
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"), nil
}

func runConfigGet(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane config get", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	jsonOutput := flags.Bool("json", false, "print raw JSON response")
	usage := "sideplane config get [--server URL] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json"); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "sideplane config get: unexpected positional arguments")
		return 1
	}

	desired, body, err := getJSON[protocol.DesiredConfig](context.Background(), serverURLValue(*serverURL), "/api/config/desired", "")
	if err != nil {
		fmt.Fprintf(stderr, "config get: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	printDesiredConfigSummary(stdout, desired)
	return 0
}

func runConfigSet(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane config set", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	provider := flags.String("provider", "", "global provider")
	model := flags.String("model", "", "global model")
	usage := "sideplane config set [--server URL] [--operator-token TOKEN] --provider PROVIDER --model MODEL"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "sideplane config set: unexpected positional arguments")
		return 1
	}
	if strings.TrimSpace(*provider) == "" || strings.TrimSpace(*model) == "" {
		fmt.Fprintln(stderr, "config set: --provider and --model are required")
		return 1
	}

	server := serverURLValue(*serverURL)
	operatorToken := operatorTokenValue(*operatorTokenFlag)
	desired, _, err := getJSON[protocol.DesiredConfig](context.Background(), server, "/api/config/desired", operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "config set: %v\n", err)
		return 1
	}
	desired.Global = protocol.ProviderModelConfig{
		Provider: strings.TrimSpace(*provider),
		Model:    strings.TrimSpace(*model),
	}

	updated, _, err := putJSON[protocol.DesiredConfig](context.Background(), server, "/api/config/desired", desired, operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "config set: %v\n", err)
		return 1
	}
	printDesiredConfigSummary(stdout, updated)
	return 0
}

func runConfigHistory(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane config history", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	limit := flags.Int("limit", 50, "maximum number of history entries")
	offset := flags.Int("offset", 0, "history page offset")
	jsonOutput := flags.Bool("json", false, "print raw JSON response")
	usage := "sideplane config history [--server URL] [--operator-token TOKEN] [--limit N] [--offset N] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json"); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	if *limit <= 0 {
		fmt.Fprintln(stderr, "config history: --limit must be positive")
		return 1
	}
	if *offset < 0 {
		fmt.Fprintln(stderr, "config history: --offset must be non-negative")
		return 1
	}
	params := url.Values{}
	params.Set("limit", strconv.Itoa(*limit))
	params.Set("offset", strconv.Itoa(*offset))
	resp, body, err := getJSON[protocol.ListDesiredConfigHistoryResponse](context.Background(), serverURLValue(*serverURL), "/api/config/desired/history?"+params.Encode(), operatorTokenValue(*operatorTokenFlag))
	if err != nil {
		fmt.Fprintf(stderr, "config history: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}
	printDesiredConfigHistoryTable(stdout, resp.History)
	return 0
}

func runConfigRevert(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane config revert", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	yes := flags.Bool("yes", false, "confirm desired config revert")
	usage := "sideplane config revert <historyId> --yes [--server URL] [--operator-token TOKEN]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "yes"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}
	historyID := strings.TrimSpace(flags.Arg(0))
	if historyID == "" {
		fmt.Fprintln(stderr, "config revert: historyId is required")
		return 1
	}
	if !*yes {
		fmt.Fprintln(stderr, "config revert: --yes is required")
		return 1
	}

	resp, _, err := postJSON[protocol.RevertDesiredConfigResponse](context.Background(), serverURLValue(*serverURL), "/api/config/desired/revert", protocol.RevertDesiredConfigRequest{HistoryID: historyID}, operatorTokenValue(*operatorTokenFlag))
	if err != nil {
		fmt.Fprintf(stderr, "config revert: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Reverted desired config to history %s.\n", historyID)
	printDesiredConfigSummary(stdout, resp.Desired)
	return 0
}

func runProbe(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane probe", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	wait := flags.Bool("wait", false, "poll until the deep probe job completes or fails")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	usage := "sideplane probe <nodeId> [--server URL] [--operator-token TOKEN] [--wait] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := parseCommandFlags(flags, args, "json", "wait"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: "+usage)
		return 1
	}

	nodeID := strings.TrimSpace(flags.Arg(0))
	if nodeID == "" {
		fmt.Fprintln(stderr, "probe: nodeId is required")
		return 1
	}

	server := serverURLValue(*serverURL)
	operatorToken := operatorTokenValue(*operatorTokenFlag)
	path := "/api/nodes/" + url.PathEscape(nodeID) + "/jobs"
	job, body, err := postJSON[protocol.Job](context.Background(), server, path, protocol.CreateJobRequest{
		Type: protocol.JobTypeDeepProbe,
	}, operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "probe: %v\n", err)
		return 1
	}

	if !*wait {
		if *jsonOutput {
			writeRawJSON(stdout, body)
			return 0
		}
		printJobSummary(stdout, job)
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	finalJob, err := waitForNodeJob(ctx, server, nodeID, job.ID, operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "probe wait: %v\n", err)
		return 1
	}
	if *jsonOutput {
		writeJSONValue(stdout, finalJob)
		return 0
	}
	printJobSummary(stdout, finalJob)
	printProbeResultSummary(stdout, finalJob)
	return 0
}

func runFleetStatus(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane fleet status", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	selector := flags.String("selector", "", "label selector with AND semantics, for example role=canary,zone=lab")
	jsonOutput := flags.Bool("json", false, "print raw JSON response")
	usage := "sideplane fleet status [--server URL] [--selector key=value[,key2=value2]] [--json]"
	if commandHelpRequested(args) {
		printCommandHelp(stdout, usage, flags)
		return 0
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "sideplane fleet status: unexpected positional arguments")
		return 1
	}

	server := serverURLValue(*serverURL)
	nodes, body, err := getNodeList(context.Background(), server, strings.TrimSpace(*selector))
	if err != nil {
		fmt.Fprintf(stderr, "fleet status: %v\n", err)
		return 1
	}

	if *jsonOutput {
		writeRawJSON(stdout, body)
		return 0
	}

	printFleetStatusTable(stdout, nodes)
	return 0
}

func getNodeList(ctx context.Context, server string, selector string) ([]cliNodeStatus, []byte, error) {
	path := "/api/nodes"
	if selector = strings.TrimSpace(selector); selector != "" {
		params := url.Values{}
		params.Set("selector", selector)
		path += "?" + params.Encode()
	}
	_, body, err := getJSON[json.RawMessage](ctx, server, path, "")
	if err != nil {
		return nil, nil, err
	}
	nodes, err := decodeNodeList(body)
	if err != nil {
		return nil, nil, err
	}
	return nodes, body, nil
}

func decodeNodeList(body []byte) ([]cliNodeStatus, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("decode response JSON: empty node list response")
	}
	if trimmed[0] == '[' {
		var nodes []cliNodeStatus
		if err := json.Unmarshal(trimmed, &nodes); err != nil {
			return nil, fmt.Errorf("decode response JSON: %w", err)
		}
		return nodes, nil
	}
	var response cliListNodesResponse
	if err := json.Unmarshal(trimmed, &response); err != nil {
		return nil, fmt.Errorf("decode response JSON: %w", err)
	}
	if response.Nodes == nil {
		return nil, fmt.Errorf("decode response JSON: nodes field is required")
	}
	return response.Nodes, nil
}

func serverURLValue(flagValue string) string {
	if strings.TrimSpace(flagValue) != "" {
		return strings.TrimSpace(flagValue)
	}
	return strings.TrimSpace(os.Getenv(serverURLEnv))
}

func operatorTokenValue(flagValue string) string {
	if strings.TrimSpace(flagValue) != "" {
		return strings.TrimSpace(flagValue)
	}
	return strings.TrimSpace(os.Getenv(auth.OperatorTokenEnv))
}

func parseCommandFlags(flags *flag.FlagSet, args []string, boolFlagNames ...string) error {
	boolFlags := make(map[string]bool, len(boolFlagNames))
	for _, name := range boolFlagNames {
		boolFlags[name] = true
	}
	return flags.Parse(reorderFlagsBeforePositionals(args, boolFlags))
}

func reorderFlagsBeforePositionals(args []string, boolFlags map[string]bool) []string {
	flagArgs := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}

		flagArgs = append(flagArgs, arg)
		name := strings.TrimLeft(arg, "-")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name = name[:eq]
		}
		if strings.Contains(arg, "=") || boolFlags[name] {
			continue
		}
		if i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return append(flagArgs, positionals...)
}

func getJSON[T any](ctx context.Context, serverURL string, path string, operatorToken string) (T, []byte, error) {
	var value T
	body, err := apiJSONRequest(ctx, http.MethodGet, serverURL, path, nil, operatorToken)
	if err != nil {
		return value, nil, err
	}
	if err := json.Unmarshal(body, &value); err != nil {
		return value, nil, fmt.Errorf("decode response JSON: %w", err)
	}
	return value, body, nil
}

func postJSON[T any](ctx context.Context, serverURL string, path string, payload any, operatorToken string) (T, []byte, error) {
	var value T
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return value, nil, fmt.Errorf("encode request JSON: %w", err)
	}
	respBody, err := apiJSONRequest(ctx, http.MethodPost, serverURL, path, &body, operatorToken)
	if err != nil {
		return value, nil, err
	}
	if err := json.Unmarshal(respBody, &value); err != nil {
		return value, nil, fmt.Errorf("decode response JSON: %w", err)
	}
	return value, respBody, nil
}

func putJSON[T any](ctx context.Context, serverURL string, path string, payload any, operatorToken string) (T, []byte, error) {
	var value T
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return value, nil, fmt.Errorf("encode request JSON: %w", err)
	}
	respBody, err := apiJSONRequest(ctx, http.MethodPut, serverURL, path, &body, operatorToken)
	if err != nil {
		return value, nil, err
	}
	if err := json.Unmarshal(respBody, &value); err != nil {
		return value, nil, fmt.Errorf("decode response JSON: %w", err)
	}
	return value, respBody, nil
}

func apiJSONRequest(ctx context.Context, method string, serverURL string, path string, body io.Reader, operatorToken string) ([]byte, error) {
	endpoint, err := apiEndpoint(serverURL, path)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("create %s request: %w", method, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := strings.TrimSpace(operatorToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	httpResp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s request: %w", strings.ToLower(method), err)
	}
	defer httpResp.Body.Close()

	respBody, readErr := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		if readErr != nil {
			return nil, fmt.Errorf("server returned status %d and response body could not be read: %w", httpResp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("server returned status %d: %s", httpResp.StatusCode, apiErrorMessage(respBody))
	}
	if readErr != nil {
		return nil, fmt.Errorf("read response body: %w", readErr)
	}
	return respBody, nil
}

func waitForNodeJob(ctx context.Context, serverURL string, nodeID string, jobID string, operatorToken string) (protocol.Job, error) {
	path := "/api/nodes/" + url.PathEscape(nodeID) + "/jobs"
	for {
		jobs, _, err := getJSON[[]protocol.Job](ctx, serverURL, path, operatorToken)
		if err != nil {
			return protocol.Job{}, err
		}
		for _, job := range jobs {
			if job.ID != jobID {
				continue
			}
			if job.Status == protocol.JobStatusCompleted || job.Status == protocol.JobStatusFailed {
				return job, nil
			}
		}

		select {
		case <-ctx.Done():
			return protocol.Job{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func waitForRollout(ctx context.Context, serverURL string, rolloutID string, operatorToken string) (protocol.Rollout, error) {
	for {
		resp, _, err := getJSON[protocol.GetRolloutResponse](ctx, serverURL, rolloutPath(rolloutID), operatorToken)
		if err != nil {
			return protocol.Rollout{}, err
		}
		if rolloutStateTerminal(resp.Rollout.State) {
			return resp.Rollout, nil
		}

		select {
		case <-ctx.Done():
			return protocol.Rollout{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func rolloutPath(rolloutID string) string {
	return "/api/rollouts/" + url.PathEscape(rolloutID)
}

func latestNodeBackup(ctx context.Context, serverURL string, nodeID string, operatorToken string) (protocol.RollbackBackupInventoryItem, error) {
	resp, _, err := getJSON[protocol.ListRollbackBackupsResponse](ctx, serverURL, nodeBackupsPath(nodeID, 1), operatorToken)
	if err != nil {
		return protocol.RollbackBackupInventoryItem{}, err
	}
	if len(resp.Backups) == 0 {
		return protocol.RollbackBackupInventoryItem{}, fmt.Errorf("no rollback backups found; run config apply first or pass --backup-ref")
	}
	return resp.Backups[0], nil
}

func nodeBackupsPath(nodeID string, limit int) string {
	path := "/api/nodes/" + url.PathEscape(nodeID) + "/backups"
	if limit > 0 {
		params := url.Values{}
		params.Set("limit", strconv.Itoa(limit))
		path += "?" + params.Encode()
	}
	return path
}

func parseCLISelector(selector string) (map[string]string, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, nil
	}
	labels := map[string]string{}
	for _, part := range strings.Split(selector, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("selector contains an empty label match")
		}
		key, value, ok := strings.Cut(part, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("selector entries must use key=value")
		}
		if _, exists := labels[key]; exists {
			return nil, fmt.Errorf("selector contains duplicate key %q", key)
		}
		labels[key] = strings.TrimSpace(value)
	}
	return labels, nil
}

func uniqueTrimmedCLIStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func printJobSummary(w io.Writer, job protocol.Job) {
	fmt.Fprintf(w, "job %s %s\n", job.ID, job.Status)
}

func printProbeResultSummary(w io.Writer, job protocol.Job) {
	if strings.TrimSpace(job.Error) != "" {
		fmt.Fprintf(w, "error: %s\n", job.Error)
		return
	}
	if strings.TrimSpace(job.ResultJSON) == "" {
		return
	}
	var result protocol.DeepProbeResult
	if err := json.Unmarshal([]byte(job.ResultJSON), &result); err != nil {
		fmt.Fprintf(w, "result: %s\n", strings.TrimSpace(job.ResultJSON))
		return
	}
	fmt.Fprintf(w, "runtimes: %s\n", runtimeSummary(result.Runtimes))
	fmt.Fprintf(w, "config snapshots: %d\n", len(result.ConfigSnapshots))
}

func writeRawJSON(w io.Writer, body []byte) {
	w.Write(body)
	if len(body) == 0 || body[len(body)-1] != '\n' {
		fmt.Fprintln(w)
	}
}

func writeJSONValue(w io.Writer, value any) {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.Encode(value)
}

func printOperatorTokenCreated(w io.Writer, resp protocol.CreateOperatorTokenResponse) {
	fmt.Fprintf(w, "operator token: %s\n", resp.Token)
	fmt.Fprintf(w, "id: %s\n", resp.OperatorToken.ID)
	fmt.Fprintf(w, "name: %s\n", resp.OperatorToken.Name)
	fmt.Fprintln(w, "shown once: yes")
}

func printOperatorTokensTable(w io.Writer, tokens []protocol.OperatorToken) {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tNAME\tCREATED\tLAST USED\tREVOKED")
	for _, token := range tokens {
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\n",
			token.ID,
			token.Name,
			timeLabel(token.CreatedAt),
			timePtrLabel(token.LastUsedAt),
			timePtrLabel(token.RevokedAt),
		)
	}
	table.Flush()
}

func printDesiredConfigSummary(w io.Writer, desired protocol.DesiredConfig) {
	fmt.Fprintf(w, "Global: %s\n", providerModelLabel(desired.Global))
	printConfigMapSummary(w, "Node overrides", desired.NodeOverrides)
	printConfigMapSummary(w, "Runtime profile overrides", desired.RuntimeProfileOverrides)
	printConfigMapSummary(w, "Node runtime profile overrides", desired.NodeRuntimeProfileOverrides)
}

func printDesiredConfigHistoryTable(w io.Writer, history []protocol.DesiredConfigHistoryEntry) {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tUPDATED\tACTOR\tHASH\tGLOBAL")
	for _, entry := range history {
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\n",
			entry.ID,
			timeLabel(entry.UpdatedAt),
			valueOrDash(entry.Actor),
			valueOrDash(entry.DesiredHash),
			providerModelLabel(entry.Config.Global),
		)
	}
	table.Flush()
}

func printEffectiveConfigPreview(w io.Writer, effective protocol.EffectiveConfigResponse, actualHashOverride string) {
	fmt.Fprintf(w, "Node: %s\n", effective.NodeID)
	fmt.Fprintf(w, "Runtime: %s/%s\n", valueOrDash(effective.RuntimeType), valueOrDash(effective.Profile))
	fmt.Fprintf(w, "Desired provider: %s\n", valueOrDash(effective.Effective.Provider))
	fmt.Fprintf(w, "Desired model: %s\n", valueOrDash(effective.Effective.Model))
	fmt.Fprintf(w, "Desired hash: %s\n", valueOrDash(effective.DesiredHash))
	actualHash := actualHashOverride
	if actualHash == "" && effective.Actual != nil {
		actualHash = effective.Actual.ConfigHash
	}
	fmt.Fprintf(w, "Actual hash: %s\n", valueOrDash(actualHash))
	fmt.Fprintln(w, "Diff:")
	if len(effective.Diff) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "  FIELD\tCHANGE\tACTUAL\tDESIRED")
	for _, entry := range effective.Diff {
		fmt.Fprintf(
			table,
			"  %s\t%s\t%s\t%s\n",
			valueOrDash(entry.Field),
			valueOrDash(entry.Change),
			valueOrDash(entry.Actual),
			valueOrDash(entry.Desired),
		)
	}
	table.Flush()
}

func printConfigApplyJobSummary(w io.Writer, job protocol.Job, requestedConfigPath string) {
	planID, mode := configApplyPlanLabels(job)
	fmt.Fprintf(w, "Plan: %s\n", valueOrDash(planID))
	fmt.Fprintf(w, "Job: %s\n", job.ID)
	fmt.Fprintf(w, "Mode: %s\n", valueOrDash(mode))
	fmt.Fprintf(w, "Status: %s\n", job.Status)
	if requestedConfigPath != "" {
		fmt.Fprintf(w, "Requested config path: %s\n", requestedConfigPath)
	}
}

func configApplyPlanLabels(job protocol.Job) (string, string) {
	if strings.TrimSpace(job.PayloadJSON) == "" {
		return "", ""
	}
	var signed protocol.SignedConfigPlan
	if err := json.Unmarshal([]byte(job.PayloadJSON), &signed); err != nil {
		return "", ""
	}
	return signed.Plan.ID, signed.Plan.Mode
}

func printConfigApplyResultSummary(w io.Writer, job protocol.Job) {
	if strings.TrimSpace(job.Error) != "" {
		fmt.Fprintf(w, "Error: %s\n", job.Error)
	}
	if strings.TrimSpace(job.ResultJSON) == "" {
		return
	}
	var result protocol.ConfigApplyResult
	if err := json.Unmarshal([]byte(job.ResultJSON), &result); err != nil {
		fmt.Fprintf(w, "Result: %s\n", strings.TrimSpace(job.ResultJSON))
		return
	}
	if result.PlanID != "" {
		fmt.Fprintf(w, "Result plan: %s\n", result.PlanID)
	}
	fmt.Fprintf(w, "Result mode: %s\n", map[bool]string{true: "dry_run", false: "live"}[result.DryRun])
	if len(result.Steps) == 0 {
		return
	}
	fmt.Fprintln(w, "Steps:")
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "  NAME\tSTATUS\tDETAIL")
	for _, step := range result.Steps {
		fmt.Fprintf(table, "  %s\t%s\t%s\n", valueOrDash(step.Name), valueOrDash(step.Status), valueOrDash(step.Detail))
	}
	table.Flush()
}

func printRestartJobSummary(w io.Writer, job protocol.Job) {
	mode, runtimeType, profile := restartJobLabels(job)
	fmt.Fprintf(w, "Job: %s\n", job.ID)
	fmt.Fprintf(w, "Mode: %s\n", valueOrDash(mode))
	fmt.Fprintf(w, "Runtime: %s\n", valueOrDash(runtimeType))
	fmt.Fprintf(w, "Profile: %s\n", valueOrDash(profile))
	fmt.Fprintf(w, "Status: %s\n", job.Status)
}

func restartJobLabels(job protocol.Job) (mode string, runtimeType string, profile string) {
	if strings.TrimSpace(job.PayloadJSON) == "" {
		return "", "", ""
	}
	var payload protocol.RestartJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return "", "", ""
	}
	mode = "live"
	if payload.DryRun {
		mode = "dry-run"
	}
	return mode, payload.RuntimeType, payload.Profile
}

func printRestartResultSummary(w io.Writer, job protocol.Job) {
	if strings.TrimSpace(job.Error) != "" {
		fmt.Fprintf(w, "Error: %s\n", job.Error)
	}
	if strings.TrimSpace(job.ResultJSON) == "" {
		return
	}
	var result protocol.RestartJobResult
	if err := json.Unmarshal([]byte(job.ResultJSON), &result); err != nil {
		fmt.Fprintf(w, "Result: %s\n", strings.TrimSpace(job.ResultJSON))
		return
	}
	fmt.Fprintf(w, "Controller: %s\n", valueOrDash(result.Controller))
	fmt.Fprintf(w, "Health: %s\n", valueOrDash(result.HealthStatus))
	if len(result.Steps) == 0 {
		return
	}
	fmt.Fprintln(w, "Steps:")
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "  NAME\tSTATUS\tDETAIL")
	for _, step := range result.Steps {
		fmt.Fprintf(table, "  %s\t%s\t%s\n", valueOrDash(step.Name), valueOrDash(step.Status), valueOrDash(step.Detail))
	}
	table.Flush()
}

func printRollbackJobSummary(w io.Writer, job protocol.Job) {
	mode, runtimeType, profile, backupRef := rollbackJobLabels(job)
	fmt.Fprintf(w, "Job: %s\n", job.ID)
	fmt.Fprintf(w, "Mode: %s\n", valueOrDash(mode))
	fmt.Fprintf(w, "Runtime: %s\n", valueOrDash(runtimeType))
	fmt.Fprintf(w, "Profile: %s\n", valueOrDash(profile))
	fmt.Fprintf(w, "Backup: %s\n", valueOrDash(backupRef))
	fmt.Fprintf(w, "Status: %s\n", job.Status)
}

func rollbackJobLabels(job protocol.Job) (mode string, runtimeType string, profile string, backupRef string) {
	if strings.TrimSpace(job.PayloadJSON) == "" {
		return "", "", "", ""
	}
	var payload protocol.RollbackJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return "", "", "", ""
	}
	mode = "live"
	if payload.DryRun {
		mode = "dry-run"
	}
	return mode, payload.RuntimeType, payload.Profile, payload.BackupRef
}

func printRollbackResultSummary(w io.Writer, job protocol.Job) {
	if strings.TrimSpace(job.Error) != "" {
		fmt.Fprintf(w, "Error: %s\n", job.Error)
	}
	if strings.TrimSpace(job.ResultJSON) == "" {
		return
	}
	var result protocol.RollbackJobResult
	if err := json.Unmarshal([]byte(job.ResultJSON), &result); err != nil {
		fmt.Fprintf(w, "Result: %s\n", strings.TrimSpace(job.ResultJSON))
		return
	}
	fmt.Fprintf(w, "Result backup: %s\n", valueOrDash(result.BackupRef))
	fmt.Fprintf(w, "Health: %s\n", valueOrDash(result.HealthStatus))
	if len(result.Steps) == 0 {
		return
	}
	fmt.Fprintln(w, "Steps:")
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "  NAME\tSTATUS\tDETAIL")
	for _, step := range result.Steps {
		fmt.Fprintf(table, "  %s\t%s\t%s\n", valueOrDash(step.Name), valueOrDash(step.Status), valueOrDash(step.Detail))
	}
	table.Flush()
}

func printConfigMapSummary(w io.Writer, label string, values map[string]protocol.ProviderModelConfig) {
	fmt.Fprintf(w, "%s:\n", label)
	if len(values) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(w, "  %s: %s\n", key, providerModelLabel(values[key]))
	}
}

func providerModelLabel(value protocol.ProviderModelConfig) string {
	provider := strings.TrimSpace(value.Provider)
	model := strings.TrimSpace(value.Model)
	if provider == "" && model == "" {
		return "(unset)"
	}
	if provider == "" {
		provider = "-"
	}
	if model == "" {
		model = "-"
	}
	return provider + " / " + model
}

func printRolloutSummary(w io.Writer, rollout protocol.Rollout) {
	fmt.Fprintf(w, "Rollout: %s\n", rollout.ID)
	fmt.Fprintf(w, "State: %s\n", rollout.State)
	fmt.Fprintf(w, "Mode: %s\n", rolloutMode(rollout))
	fmt.Fprintf(w, "Runtime: %s\n", rolloutRuntimeLabel(rollout))
	fmt.Fprintf(w, "Target: %s\n", providerModelLabel(rollout.Spec.Target))
	fmt.Fprintf(w, "Batch size: %d\n", rollout.Spec.BatchSize)
	fmt.Fprintf(w, "Nodes: %s\n", rolloutNodeIDsLabel(rollout.Spec.NodeIDs))
	if strings.TrimSpace(rollout.PauseReason) != "" {
		fmt.Fprintf(w, "Pause reason: %s\n", rollout.PauseReason)
	}
}

func printRolloutsTable(w io.Writer, rollouts []protocol.Rollout) {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "ROLLOUT\tSTATE\tMODE\tTARGET\tRUNTIME\tBATCHES\tUPDATED")
	for _, rollout := range rollouts {
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			valueOrDash(rollout.ID),
			valueOrDash(string(rollout.State)),
			rolloutMode(rollout),
			providerModelLabel(rollout.Spec.Target),
			rolloutRuntimeLabel(rollout),
			rolloutBatchProgressLabel(rollout),
			timeLabel(rollout.UpdatedAt),
		)
	}
	table.Flush()
}

func printRolloutDetail(w io.Writer, rollout protocol.Rollout) {
	printRolloutSummary(w, rollout)
	if len(rollout.FailingNodeIDs) > 0 {
		fmt.Fprintf(w, "Failing nodes: %s\n", strings.Join(rollout.FailingNodeIDs, ","))
	}
	fmt.Fprintln(w, "Batches:")
	if len(rollout.Batches) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	batchTable := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(batchTable, "  INDEX\tSTATE\tNODES")
	for _, batch := range rollout.Batches {
		fmt.Fprintf(batchTable, "  %d\t%s\t%s\n", batch.Index, batch.State, strings.Join(batch.NodeIDs, ","))
	}
	batchTable.Flush()

	fmt.Fprintln(w, "Nodes:")
	nodeTable := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(nodeTable, "  BATCH\tNODE\tSTATE\tJOB\tERROR")
	for _, batch := range rollout.Batches {
		for _, nodeID := range batch.NodeIDs {
			progress := batch.Nodes[nodeID]
			fmt.Fprintf(
				nodeTable,
				"  %d\t%s\t%s\t%s\t%s\n",
				batch.Index,
				valueOrDash(nodeID),
				valueOrDash(string(progress.State)),
				valueOrDash(progress.JobID),
				valueOrDash(progress.LastError),
			)
		}
	}
	nodeTable.Flush()
}

func rolloutMode(rollout protocol.Rollout) string {
	if rollout.Spec.Live {
		return "live"
	}
	return "dry-run"
}

func rolloutRuntimeLabel(rollout protocol.Rollout) string {
	runtimeType := strings.TrimSpace(rollout.Spec.RuntimeType)
	profile := strings.TrimSpace(rollout.Spec.Profile)
	if runtimeType == "" && profile == "" {
		return "-"
	}
	if runtimeType == "" {
		runtimeType = "-"
	}
	if profile == "" {
		return runtimeType
	}
	return runtimeType + "/" + profile
}

func rolloutNodeIDsLabel(nodeIDs []string) string {
	if len(nodeIDs) == 0 {
		return "-"
	}
	return strings.Join(nodeIDs, ",")
}

func rolloutBatchProgressLabel(rollout protocol.Rollout) string {
	if len(rollout.Batches) == 0 {
		return "0/0"
	}
	completed := 0
	for _, batch := range rollout.Batches {
		if batch.State == protocol.RolloutBatchStateCompleted {
			completed++
		}
	}
	return fmt.Sprintf("%d/%d", completed, len(rollout.Batches))
}

func rolloutStateTerminal(state protocol.RolloutState) bool {
	switch state {
	case protocol.RolloutStateCompleted, protocol.RolloutStateAborted, protocol.RolloutStateFailed:
		return true
	default:
		return false
	}
}

func printFleetStatusTable(w io.Writer, nodes []cliNodeStatus) {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "NODE ID\tSTATE\tRUNTIMES\tDRIFT\tHEARTBEAT")
	for _, node := range nodes {
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\n",
			node.NodeID,
			node.State,
			runtimeSummary(node.Runtimes),
			yesNo(node.Drift),
			ageLabel(node.LastHeartbeatAt),
		)
	}
	table.Flush()
}

func printNodeInspect(w io.Writer, node cliNodeStatus) {
	fmt.Fprintf(w, "Node: %s\n", node.NodeID)
	fmt.Fprintf(w, "State: %s\n", node.State)
	fmt.Fprintf(w, "Hostname: %s\n", valueOrDash(node.Hostname))
	heartbeat := "-"
	if !node.LastHeartbeatAt.IsZero() {
		heartbeat = node.LastHeartbeatAt.Format(time.RFC3339) + " (" + ageLabel(node.LastHeartbeatAt) + ")"
	}
	fmt.Fprintf(w, "Heartbeat: %s\n", heartbeat)
	fmt.Fprintf(w, "Sidecar: %s\n", valueOrDash(node.SidecarVersion))
	fmt.Fprintf(w, "Config hash: %s\n", valueOrDash(node.ConfigHash))
	fmt.Fprintf(w, "Drift: %s\n", yesNo(node.Drift))
	fmt.Fprintf(w, "Labels: %s\n", labelsInline(node.Labels))
	fmt.Fprintf(w, "Last error: %s\n", valueOrDash(node.LastError))
	fmt.Fprintln(w, "Runtimes:")
	if len(node.Runtimes) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "  NAME\tTYPE\tSTATE\tVERSION\tPROVIDER\tMODEL\tCONFIG HASH\tWARNINGS\tLAST ERROR")
	for _, runtime := range node.Runtimes {
		fmt.Fprintf(
			table,
			"  %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			valueOrDash(runtime.Name),
			valueOrDash(runtime.Type),
			valueOrDash(runtime.State),
			valueOrDash(runtime.Version),
			valueOrDash(runtime.Provider),
			valueOrDash(runtime.Model),
			valueOrDash(runtime.ConfigHash),
			warningsLabel(runtime.Warnings),
			valueOrDash(runtime.LastError),
		)
	}
	table.Flush()
}

func printNodeLabels(w io.Writer, resp protocol.NodeLabelsResponse) {
	fmt.Fprintf(w, "Node: %s\n", resp.NodeID)
	fmt.Fprintln(w, "Labels:")
	if len(resp.Labels) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	for _, key := range sortedLabelKeys(resp.Labels) {
		fmt.Fprintf(w, "  %s=%s\n", key, resp.Labels[key])
	}
}

func labelsInline(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(labels))
	for _, key := range sortedLabelKeys(labels) {
		parts = append(parts, key+"="+labels[key])
	}
	return strings.Join(parts, ",")
}

func sortedLabelKeys(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func warningsLabel(warnings []string) string {
	if len(warnings) == 0 {
		return "-"
	}
	return strings.Join(warnings, "; ")
}

func printJobsTable(w io.Writer, jobs []protocol.Job) {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "JOB ID\tTYPE\tSTATUS\tCREATED\tFINISHED/ERROR")
	for _, job := range jobs {
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\n",
			job.ID,
			job.Type,
			job.Status,
			timeLabel(job.CreatedAt),
			jobFinishedOrError(job),
		)
	}
	table.Flush()
}

func printAuditTable(w io.Writer, events []protocol.AuditEvent) {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "CREATED\tACTOR\tACTION\tNODE\tDETAIL")
	for _, event := range events {
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\n",
			timeLabel(event.CreatedAt),
			valueOrDash(event.Actor),
			valueOrDash(event.Action),
			valueOrDash(event.TargetNode),
			valueOrDash(spconfig.RedactString(event.Detail)),
		)
	}
	table.Flush()
}

func printBackupsTable(w io.Writer, backups []protocol.RollbackBackupInventoryItem) {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "REF\tRUNTIME\tPROFILE\tCONFIG HASH\tCREATED\tSOURCE JOB")
	for _, backup := range backups {
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			valueOrDash(backup.Ref),
			valueOrDash(backup.RuntimeType),
			valueOrDash(backup.Profile),
			valueOrDash(backup.ConfigHash),
			timeLabel(backup.CreatedAt),
			valueOrDash(backup.SourceJobID),
		)
	}
	table.Flush()
}

func validCLIJobStatus(status protocol.JobStatus) bool {
	switch status {
	case protocol.JobStatusPending, protocol.JobStatusClaimed, protocol.JobStatusCompleted, protocol.JobStatusFailed:
		return true
	default:
		return false
	}
}

func jobFinishedOrError(job protocol.Job) string {
	if strings.TrimSpace(job.Error) != "" {
		return job.Error
	}
	if !job.FinishedAt.IsZero() {
		return timeLabel(job.FinishedAt)
	}
	return "-"
}

func timeLabel(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.Format(time.RFC3339)
}

func timePtrLabel(ts *time.Time) string {
	if ts == nil {
		return "-"
	}
	return timeLabel(*ts)
}

func runtimeSummary(runtimes []protocol.RuntimeStatus) string {
	if len(runtimes) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(runtimes))
	for _, runtime := range runtimes {
		name := strings.TrimSpace(runtime.Name)
		if name == "" {
			name = strings.TrimSpace(runtime.Type)
		}
		if name == "" {
			name = "runtime"
		}
		if model := strings.TrimSpace(runtime.Model); model != "" {
			name += ":" + model
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, ",")
}

func valueOrDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func ageLabel(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	elapsed := time.Since(ts)
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds ago", int(elapsed.Seconds()))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(elapsed.Hours()/24))
	}
}

func runEnrollmentCreate(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane enrollment create", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	expiresIn := flags.Duration("expires-in", 0, "optional duration before the token expires")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	usage := "sideplane enrollment create [--server URL] [--operator-token TOKEN] [--expires-in DURATION]"
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

	resp, err := createEnrollmentToken(context.Background(), serverURLValue(*serverURL), *expiresIn, operatorTokenValue(*operatorTokenFlag))
	if err != nil {
		fmt.Fprintf(stderr, "create enrollment token: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "enrollment token: %s\n", resp.Token)
	fmt.Fprintf(stdout, "expires at: %s\n", resp.ExpiresAt.Format(time.RFC3339))
	return 0
}

func createEnrollmentToken(ctx context.Context, serverURL string, expiresIn time.Duration, operatorToken string) (protocol.CreateEnrollmentTokenResponse, error) {
	endpoint, err := apiEndpoint(serverURL, "/api/enrollment-tokens")
	if err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, err
	}

	var body bytes.Buffer
	if expiresIn > 0 {
		if err := json.NewEncoder(&body).Encode(protocol.CreateEnrollmentTokenRequest{
			ExpiresAt: time.Now().UTC().Add(expiresIn),
		}); err != nil {
			return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("encode enrollment token request: %w", err)
		}
	} else {
		body.WriteString("{}")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("create enrollment token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(operatorToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	httpResp, err := httpClient.Do(req)
	if err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("post enrollment token request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 1024))
		return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("server returned status %d: %s", httpResp.StatusCode, apiErrorMessage(body))
	}

	var resp protocol.CreateEnrollmentTokenResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("decode enrollment token response: %w", err)
	}
	if strings.TrimSpace(resp.Token) == "" || resp.ExpiresAt.IsZero() {
		return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("server response missing token or expiry")
	}
	return resp, nil
}

func apiErrorMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var apiErr protocol.APIError
	if err := json.Unmarshal(body, &apiErr); err == nil && strings.TrimSpace(apiErr.Message) != "" {
		return spconfig.RedactString(apiErr.Message)
	}
	return spconfig.RedactString(trimmed)
}

func apiEndpoint(serverURL string, path string) (string, error) {
	if strings.TrimSpace(serverURL) == "" {
		return "", fmt.Errorf("server URL is required")
	}
	serverURL = strings.TrimSpace(serverURL)
	if !strings.Contains(serverURL, "://") {
		serverURL = "http://" + serverURL
	}
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("parse server URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("server URL must be absolute: %q", serverURL)
	}
	pathPart, rawQuery, hasQuery := strings.Cut(path, "?")
	endpoint, err := url.JoinPath(strings.TrimRight(serverURL, "/"), pathPart)
	if err != nil {
		return "", fmt.Errorf("build API endpoint: %w", err)
	}
	if hasQuery {
		endpoint += "?" + rawQuery
	}
	return endpoint, nil
}
