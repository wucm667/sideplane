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
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wucm667/sideplane/internal/auth"
	"github.com/wucm667/sideplane/pkg/protocol"
)

const version = "dev"
const serverURLEnv = "SIDEPLANE_SERVER_URL"

var cliStdin io.Reader = os.Stdin

type cliNodeStatus struct {
	protocol.NodeStatus
	Drift bool `json:"drift"`
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
		fmt.Fprintf(stdout, "sideplane %s\n", version)
		return 0
	}

	switch args[0] {
	case "fleet":
		if len(args) >= 2 && args[1] == "status" {
			return runFleetStatus(args[2:], stdout, stderr)
		}
	case "probe":
		return runProbe(args[1:], stdout, stderr)
	case "config":
		if len(args) >= 2 && args[1] == "get" {
			return runConfigGet(args[2:], stdout, stderr)
		}
		if len(args) >= 2 && args[1] == "set" {
			return runConfigSet(args[2:], stdout, stderr)
		}
	case "node":
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
  config get          Show desired configuration
  config set          Update global desired configuration
  node remove <id>    Remove a node from the fleet
  enrollment create   Create a one-time enrollment token
  version             Print version
`)
}

func runNodeRemove(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane node remove", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	yes := flags.Bool("yes", false, "skip confirmation")
	if err := parseCommandFlags(flags, args, "yes"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: sideplane node remove <nodeId> [--server URL] [--operator-token TOKEN] [--yes]")
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

func runProbe(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane probe", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL; can also be set with SIDEPLANE_SERVER_URL")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	wait := flags.Bool("wait", false, "poll until the deep probe job completes or fails")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := parseCommandFlags(flags, args, "json", "wait"); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: sideplane probe <nodeId> [--server URL] [--operator-token TOKEN] [--wait] [--json]")
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
	jsonOutput := flags.Bool("json", false, "print raw JSON response")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "sideplane fleet status: unexpected positional arguments")
		return 1
	}

	server := serverURLValue(*serverURL)
	nodes, body, err := getJSON[[]cliNodeStatus](context.Background(), server, "/api/nodes", "")
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
		return nil, fmt.Errorf("server returned status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(respBody)))
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

func printDesiredConfigSummary(w io.Writer, desired protocol.DesiredConfig) {
	fmt.Fprintf(w, "Global: %s\n", providerModelLabel(desired.Global))
	printConfigMapSummary(w, "Node overrides", desired.NodeOverrides)
	printConfigMapSummary(w, "Runtime profile overrides", desired.RuntimeProfileOverrides)
	printConfigMapSummary(w, "Node runtime profile overrides", desired.NodeRuntimeProfileOverrides)
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

	serverURL := flags.String("server", "", "Sideplane server URL")
	expiresIn := flags.Duration("expires-in", 0, "optional duration before the token expires")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	operatorToken := strings.TrimSpace(*operatorTokenFlag)
	if operatorToken == "" {
		operatorToken = strings.TrimSpace(os.Getenv(auth.OperatorTokenEnv))
	}

	resp, err := createEnrollmentToken(context.Background(), *serverURL, *expiresIn, operatorToken)
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
		return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("server returned status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(body)))
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
	endpoint, err := url.JoinPath(strings.TrimRight(serverURL, "/"), path)
	if err != nil {
		return "", fmt.Errorf("build API endpoint: %w", err)
	}
	return endpoint, nil
}
