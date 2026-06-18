package hermes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wucm667/sideplane/pkg/adapters"
	spconfig "github.com/wucm667/sideplane/pkg/config"
	"github.com/wucm667/sideplane/pkg/protocol"
)

// AdapterName is the human-readable name of the Hermes Agent runtime.
const AdapterName = "hermes"

// AdapterType is the runtime type identifier.
const AdapterType = "hermes"

// Adapter is a lightweight runtime adapter for Hermes Agent.
type Adapter struct {
	lookup          func(string) (string, error)
	runCommand      func(context.Context, string, ...string) ([]byte, error)
	configPaths     []string
	container       string
	serviceUnitName string
	allowLive       bool
	getenv          func(string) string
}

// Option configures a Hermes adapter.
type Option func(*Adapter)

// WithConfigPaths configures the read-only config path search list.
func WithConfigPaths(paths ...string) Option {
	return func(a *Adapter) {
		a.configPaths = append([]string(nil), paths...)
	}
}

// WithDockerContainer configures a read-only Docker container log source.
func WithDockerContainer(container string) Option {
	return func(a *Adapter) {
		a.container = strings.TrimSpace(container)
	}
}

// NewAdapter returns a Hermes Agent runtime adapter.
func NewAdapter(opts ...Option) *Adapter {
	a := &Adapter{
		lookup:     exec.LookPath,
		runCommand: runCommand,
		getenv:     os.Getenv,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Name returns the adapter name.
func (a *Adapter) Name() string {
	return AdapterName
}

// Type returns the adapter type.
func (a *Adapter) Type() string {
	return AdapterType
}

// Detect reports whether Hermes appears to be installed on this node.
// It checks both the hermes command and known read-only config locations.
func (a *Adapter) Detect(ctx context.Context) (bool, error) {
	lookup := a.lookup
	if lookup == nil {
		lookup = exec.LookPath
	}
	if _, err := lookup("hermes"); err == nil {
		return true, nil
	}

	path, err := a.findConfigPath()
	if err != nil {
		return false, err
	}
	if path != "" {
		return true, nil
	}
	container := a.dockerContainer()
	if container == "" {
		return false, nil
	}
	return a.detectDockerContainer(ctx, container)
}

// Status returns a minimal RuntimeStatus for Hermes Agent.
// It reads local configuration only when a safe, readable config path is found.
func (a *Adapter) Status(ctx context.Context) (protocol.RuntimeStatus, error) {
	present, err := a.Detect(ctx)
	if err != nil {
		return adapters.StatusFromError(AdapterName, AdapterType, err), nil
	}
	if !present {
		return protocol.RuntimeStatus{}, nil
	}
	status := protocol.RuntimeStatus{
		Name:  AdapterName,
		Type:  AdapterType,
		State: "present",
	}

	snapshot, err := a.snapshot(ctx)
	if err != nil {
		status.State = "error"
		status.LastError = err.Error()
		return status, nil
	}
	if snapshot != nil {
		status.Provider = snapshot.Provider
		status.Model = snapshot.Model
		status.ConfigHash = snapshot.ConfigHash
	}
	return status, nil
}

// ConfigSnapshots returns read-only Hermes config snapshots.
func (a *Adapter) ConfigSnapshots(ctx context.Context) ([]protocol.RuntimeConfigSnapshot, error) {
	present, err := a.Detect(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	snapshot, err := a.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return []protocol.RuntimeConfigSnapshot{
			{
				RuntimeName: AdapterName,
				RuntimeType: AdapterType,
				Source:      "adapter",
				Warnings:    []string{"hermes config file not found in configured or default read-only search paths"},
			},
		}, nil
	}
	return []protocol.RuntimeConfigSnapshot{*snapshot}, nil
}

func (a *Adapter) snapshot(ctx context.Context) (*protocol.RuntimeConfigSnapshot, error) {
	path, err := a.findConfigPath()
	if err != nil {
		return nil, err
	}
	var fileSnapshot *protocol.RuntimeConfigSnapshot
	if path == "" {
		return a.snapshotFromDocker(ctx)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read hermes config: %w", err)
	}
	values := extractConfigValues(contents)
	provider, model := extractProviderModel(values)
	// Prefer the explicit top-level model block (model.provider / model.default)
	// when present; it is the authoritative source for the active provider/model.
	if blockProvider, blockModel, ok := ModelFields(contents); ok {
		provider, model = blockProvider, blockModel
	}
	warnings := []string{}
	if provider == "" && model == "" {
		warnings = append(warnings, "provider/model not found in hermes config")
	}
	sum := sha256.Sum256(contents)
	snapshot := spconfig.NewRuntimeConfigSnapshot(spconfig.SnapshotInput{
		RuntimeName: AdapterName,
		RuntimeType: AdapterType,
		ConfigPath:  path,
		Source:      "file",
		Provider:    provider,
		Model:       model,
		ConfigHash:  "sha256:" + hex.EncodeToString(sum[:]),
		Warnings:    warnings,
	})
	fileSnapshot = &snapshot
	if fileSnapshot.Provider != "" && fileSnapshot.Model != "" {
		return fileSnapshot, nil
	}
	dockerSnapshot, err := a.snapshotFromDocker(ctx)
	if err != nil {
		return nil, err
	}
	if dockerSnapshot != nil {
		return dockerSnapshot, nil
	}
	return fileSnapshot, nil
}

func (a *Adapter) snapshotFromDocker(ctx context.Context) (*protocol.RuntimeConfigSnapshot, error) {
	container := a.dockerContainer()
	if container == "" {
		return nil, nil
	}
	logs, err := a.runDocker(ctx, "logs", "--tail", "200", container)
	if err != nil {
		return nil, fmt.Errorf("read hermes docker logs: %w", err)
	}
	values := extractConfigValues(logs)
	provider, model := extractProviderModel(values)
	warnings := []string{}
	if provider == "" && model == "" {
		warnings = append(warnings, "provider/model not found in hermes docker logs")
	}
	configHash, hashErr := a.dockerConfigHash(ctx, container, logs)
	if hashErr != nil {
		warnings = append(warnings, hashErr.Error())
	}
	snapshot := spconfig.NewRuntimeConfigSnapshot(spconfig.SnapshotInput{
		RuntimeName: AdapterName,
		RuntimeType: AdapterType,
		ConfigPath:  "docker://" + container + "/logs",
		Source:      "docker_logs",
		Provider:    provider,
		Model:       model,
		ConfigHash:  configHash,
		Warnings:    warnings,
	})
	return &snapshot, nil
}

func (a *Adapter) dockerConfigHash(ctx context.Context, container string, fallback []byte) (string, error) {
	label, err := a.runDocker(ctx, "inspect", "--format", `{{index .Config.Labels "com.docker.compose.config-hash"}}`, container)
	if err == nil {
		value := strings.TrimSpace(string(label))
		if value != "" && value != "<no value>" {
			return "sha256:" + value, nil
		}
	}
	sum := sha256.Sum256(fallback)
	if err != nil {
		return "sha256:" + hex.EncodeToString(sum[:]), fmt.Errorf("docker compose config hash unavailable")
	}
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (a *Adapter) dockerContainer() string {
	container := strings.TrimSpace(a.container)
	if container != "" {
		return container
	}
	getenv := a.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	return strings.TrimSpace(getenv("SIDEPLANE_HERMES_DOCKER_CONTAINER"))
}

func (a *Adapter) detectDockerContainer(ctx context.Context, container string) (bool, error) {
	if _, err := a.runDocker(ctx, "inspect", "--type", "container", container); err != nil {
		return false, nil
	}
	return true, nil
}

func (a *Adapter) runDocker(ctx context.Context, args ...string) ([]byte, error) {
	runner := a.runCommand
	if runner == nil {
		runner = runCommand
	}
	return runner(ctx, "docker", args...)
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

func (a *Adapter) findConfigPath() (string, error) {
	for _, path := range a.configSearchPaths() {
		resolved, err := expandPath(path)
		if err != nil {
			return "", err
		}
		if resolved == "" {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil {
			if os.IsNotExist(err) || os.IsPermission(err) {
				continue
			}
			return "", fmt.Errorf("stat hermes config: %w", err)
		}
		if info.Mode().IsRegular() {
			return resolved, nil
		}
	}
	return "", nil
}

func (a *Adapter) configSearchPaths() []string {
	paths := append([]string(nil), a.configPaths...)
	getenv := a.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	for _, key := range []string{"SIDEPLANE_HERMES_CONFIG_PATHS", "HERMES_CONFIG_PATH", "HERMES_CONFIG"} {
		paths = append(paths, splitPathList(getenv(key))...)
	}
	paths = append(paths, defaultConfigPaths()...)
	return dedupeStrings(paths)
}

func defaultConfigPaths() []string {
	return []string{
		"~/.hermes/config.yaml",
		"~/.hermes/config.yml",
		"~/.hermes/config.json",
		"~/.hermes/config.toml",
		"~/.config/hermes/config.json",
		"~/.config/hermes/config.yaml",
		"~/.config/hermes/config.yml",
		"~/.config/hermes/config.toml",
		"~/.config/hermes/hermes.env",
		"/etc/hermes/config.json",
		"/etc/hermes/config.yaml",
		"/etc/hermes/config.yml",
		"/etc/hermes/config.toml",
		"/etc/hermes/hermes.env",
		"/opt/hermes/config.json",
		"/opt/hermes/config.yaml",
		"/opt/hermes/config.yml",
		"/opt/hermes/config.toml",
		"/opt/hermes/hermes.env",
	}
}

func splitPathList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
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

func expandPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func dedupeStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func extractConfigValues(contents []byte) map[string]string {
	values := map[string]string{}
	var decoded any
	if err := json.Unmarshal(contents, &decoded); err == nil {
		collectJSONValues("", decoded, values)
	}

	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "{") {
			var entry any
			if err := json.Unmarshal([]byte(line), &entry); err == nil {
				collectJSONValues("", entry, values)
				continue
			}
		}
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimSpace(line)
		line = strings.Trim(line, "\"'")
		if key, value, ok := splitConfigAssignment(line, "="); ok {
			values[key] = value
			continue
		}
		if key, value, ok := splitConfigAssignment(line, ":"); ok {
			values[key] = value
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func collectJSONValues(prefix string, value any, out map[string]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			collectJSONValues(path, nested, out)
		}
	case []any:
		for _, nested := range typed {
			collectJSONValues(prefix, nested, out)
		}
	case string:
		if prefix != "" {
			out[prefix] = strings.TrimSpace(typed)
		}
	case float64, bool:
		if prefix != "" {
			out[prefix] = fmt.Sprint(typed)
		}
	}
}

func splitConfigAssignment(line string, separator string) (string, string, bool) {
	idx := strings.Index(line, separator)
	if idx <= 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:idx])
	value := strings.TrimSpace(line[idx+len(separator):])
	key = strings.Trim(key, "\"'")
	value = strings.Trim(value, " ,\"'")
	if key == "" || value == "" || !looksLikeConfigKey(key) {
		return "", "", false
	}
	return key, value, true
}

func looksLikeConfigKey(key string) bool {
	for _, r := range key {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func extractProviderModel(values map[string]string) (string, string) {
	provider := firstMatchingValue(values, isProviderKey)
	model := firstMatchingValue(values, isModelKey)
	if modelProvider, modelName, ok := splitProviderModel(model); ok {
		if provider == "" {
			provider = modelProvider
		}
		if provider == modelProvider {
			model = modelName
		}
	}
	return provider, model
}

func firstMatchingValue(values map[string]string, matches func(string) bool) string {
	for _, key := range sortedConfigKeys(values) {
		if matches(key) {
			return strings.TrimSpace(values[key])
		}
	}
	return ""
}

func sortedConfigKeys(values map[string]string) []string {
	exact := []string{}
	preferred := []string{}
	fallback := []string{}
	for key := range values {
		normalized := normalizeConfigKey(key)
		switch normalized {
		case "provider", "model":
			exact = append(exact, key)
		default:
			if strings.Contains(normalized, "llm") || strings.Contains(normalized, "agent") || strings.Contains(normalized, "runtime") || strings.Contains(normalized, "chat") {
				preferred = append(preferred, key)
			} else {
				fallback = append(fallback, key)
			}
		}
	}
	sort.Strings(exact)
	sort.Strings(preferred)
	sort.Strings(fallback)
	return append(append(exact, preferred...), fallback...)
}

func isProviderKey(key string) bool {
	normalized := normalizeConfigKey(key)
	return normalized == "provider" || strings.HasSuffix(normalized, "_provider") || strings.HasSuffix(normalized, ".provider")
}

func isModelKey(key string) bool {
	normalized := normalizeConfigKey(key)
	if strings.Contains(normalized, "device_model") || strings.Contains(normalized, "embedding_model") {
		return false
	}
	return normalized == "model" || strings.HasSuffix(normalized, "_model") || strings.HasSuffix(normalized, ".model")
}

func normalizeConfigKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	return key
}

func splitProviderModel(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}
