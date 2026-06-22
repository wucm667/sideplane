package openclaw

import (
	"bytes"
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

// AdapterName is the human-readable name of the OpenClaw runtime.
const AdapterName = "openclaw"

// AdapterType is the runtime type identifier.
const AdapterType = "openclaw"

// Adapter is a lightweight runtime adapter for OpenClaw.
type Adapter struct {
	lookup             func(string) (string, error)
	runCommand         func(context.Context, string, ...string) ([]byte, error)
	configPaths        []string
	defaultConfigPaths []string
	container          string
	serviceUnitName    string
	restartSudo        bool
	allowLive          bool
	getenv             func(string) string
}

// Option configures an OpenClaw adapter.
type Option func(*Adapter)

// WithConfigPaths configures the read-only config path search list.
func WithConfigPaths(paths ...string) Option {
	return func(a *Adapter) {
		a.configPaths = append([]string(nil), paths...)
	}
}

// WithDockerContainer configures an allowlisted OpenClaw Docker restart target.
func WithDockerContainer(container string) Option {
	return func(a *Adapter) {
		a.container = strings.TrimSpace(container)
	}
}

// NewAdapter returns an OpenClaw runtime adapter.
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

// Detect reports whether OpenClaw appears to be installed on this node.
// It checks both the openclaw command and safe read-only config locations.
func (a *Adapter) Detect(ctx context.Context) (bool, error) {
	lookup := a.lookup
	if lookup == nil {
		lookup = exec.LookPath
	}
	if _, err := lookup("openclaw"); err == nil {
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

// Status returns a minimal RuntimeStatus for OpenClaw.
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

// ConfigSnapshots returns read-only OpenClaw config snapshots.
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
		return []protocol.RuntimeConfigSnapshot{{
			RuntimeName: AdapterName,
			RuntimeType: AdapterType,
			Source:      "adapter",
			Warnings:    []string{"openclaw config file not found in configured or default read-only search paths"},
		}}, nil
	}
	return []protocol.RuntimeConfigSnapshot{*snapshot}, nil
}

func (a *Adapter) snapshot(_ context.Context) (*protocol.RuntimeConfigSnapshot, error) {
	path, err := a.findConfigPath()
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read openclaw config: %w", err)
	}
	if err := validateConfigSyntax(path, contents); err != nil {
		return nil, err
	}
	provider, model := extractProviderModel(extractConfigValues(contents))
	warnings := []string{}
	if provider != "" || model != "" {
		if provider == "" || model == "" {
			provider = ""
			model = ""
			warnings = append(warnings, "provider/model incomplete in openclaw config")
		} else if err := spconfig.ValidateProviderModelSelection(protocol.ProviderModelConfig{Provider: provider, Model: model}); err != nil {
			provider = ""
			model = ""
			warnings = append(warnings, "provider/model rejected in openclaw config: "+err.Error())
		}
	}
	if provider == "" && model == "" && len(warnings) == 0 {
		provider = ""
		model = ""
		warnings = append(warnings, "provider/model not found in openclaw config")
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
	return &snapshot, nil
}

// ProviderModelFields extracts provider/model values from an OpenClaw config.
// found is true only when both fields are present.
func ProviderModelFields(contents []byte) (provider string, model string, found bool) {
	provider, model = extractProviderModel(extractConfigValues(contents))
	return provider, model, provider != "" && model != ""
}

func validateConfigSyntax(path string, contents []byte) error {
	if strings.ToLower(filepath.Ext(path)) != ".json" {
		return nil
	}
	trimmed := bytes.TrimSpace(contents)
	if len(trimmed) == 0 {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return fmt.Errorf("parse openclaw JSON config: %w", err)
	}
	return nil
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
	return strings.TrimSpace(getenv("SIDEPLANE_OPENCLAW_DOCKER_CONTAINER"))
}

func (a *Adapter) detectDockerContainer(ctx context.Context, container string) (bool, error) {
	if _, err := a.runDocker(ctx, "inspect", "--type", "container", container); err != nil {
		return false, nil
	}
	return true, nil
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
			return "", fmt.Errorf("stat openclaw config: %w", err)
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
	paths = append(paths, splitPathList(getenv("SIDEPLANE_OPENCLAW_CONFIG_PATHS"))...)
	defaults := defaultConfigPaths()
	if a.defaultConfigPaths != nil {
		defaults = append([]string(nil), a.defaultConfigPaths...)
	}
	paths = append(paths, defaults...)
	return dedupeStrings(paths)
}

func defaultConfigPaths() []string {
	return []string{
		"~/.openclaw/config.json",
		"~/.openclaw/config.yaml",
		"~/.openclaw/config.yml",
		"~/.openclaw/config.toml",
		"~/.config/openclaw/config.json",
		"~/.config/openclaw/config.yaml",
		"~/.config/openclaw/config.yml",
		"~/.config/openclaw/config.toml",
		"/etc/openclaw/config.json",
		"/etc/openclaw/config.yaml",
		"/etc/openclaw/config.yml",
		"/etc/openclaw/config.toml",
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
		if isSecretLikeKey(key) {
			continue
		}
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

func isSecretLikeKey(key string) bool {
	normalized := normalizeConfigKey(key)
	return strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "api_key") ||
		strings.Contains(normalized, "apikey") ||
		strings.HasSuffix(normalized, "_key") ||
		strings.HasSuffix(normalized, ".key")
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
