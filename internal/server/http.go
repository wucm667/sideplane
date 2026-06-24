package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wucm667/sideplane/internal/audit"
	"github.com/wucm667/sideplane/internal/auth"
	"github.com/wucm667/sideplane/internal/buildinfo"
	rolloutengine "github.com/wucm667/sideplane/internal/rollout"
	"github.com/wucm667/sideplane/internal/store"
	spconfig "github.com/wucm667/sideplane/pkg/config"
	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	"github.com/wucm667/sideplane/pkg/protocol"
)

const (
	defaultEnrollmentTokenTTL = time.Hour
	defaultJSONBodyLimit      = int64(1 << 20)
	largeJSONBodyLimit        = int64(4 << 20)
	defaultBackupListLimit    = 50
	maxBackupListLimit        = 500
	rolloutStartAtPastLimit   = 24 * time.Hour
	rolloutStartAtFutureLimit = 365 * 24 * time.Hour
	// maxAuditExportLimit matches the store's filtered audit listing cap.
	maxAuditExportLimit = 500
)

var supportedExpectedRuntimeVersionTypes = []string{"hermes", "openclaw"}

// NewHandler returns the Sideplane server HTTP handler.
func NewHandler() http.Handler {
	return NewHandlerWithStore(store.NewMemoryNodeStore())
}

// NewHandlerWithStore returns a Sideplane server HTTP handler backed by store.
func NewHandlerWithStore(nodeStore store.Store) http.Handler {
	handler, err := NewHandlerWithStoreAndFreshnessPolicy(nodeStore, DefaultFreshnessPolicy())
	if err != nil {
		panic(err)
	}
	return handler
}

// NewHandlerWithStoreAndFreshnessPolicy returns a Sideplane server HTTP handler backed by store.
func NewHandlerWithStoreAndFreshnessPolicy(nodeStore store.Store, freshness FreshnessPolicy) (http.Handler, error) {
	return NewHandlerWithStoreAndFreshnessPolicyAndOperatorToken(nodeStore, freshness, "")
}

// NewHandlerWithStoreAndFreshnessPolicyAndOperatorToken returns a server HTTP
// handler with optional bearer-token auth for mutating operator endpoints.
func NewHandlerWithStoreAndFreshnessPolicyAndOperatorToken(nodeStore store.Store, freshness FreshnessPolicy, operatorToken string) (http.Handler, error) {
	return NewHandlerWithConfig(HandlerConfig{
		Store:         nodeStore,
		Freshness:     freshness,
		OperatorToken: operatorToken,
	})
}

// HandlerConfig configures the Sideplane server HTTP handler.
type HandlerConfig struct {
	Store                           store.Store
	Freshness                       FreshnessPolicy
	OperatorToken                   string
	AllowUnauthenticatedOperatorAPI bool
	SigningKeyPath                  string
	SigningKeyPair                  spcrypto.KeyPair
	DisableSigningKey               bool
	SecretKey                       []byte
	RateLimits                      RateLimitConfig
	Events                          *EventHub
	Metrics                         *Metrics
	SchemaVersion                   int
	StartedAt                       time.Time
	Now                             func() time.Time
	Logger                          *slog.Logger
}

// NewHandlerWithConfig returns a server HTTP handler with explicit dependencies.
func NewHandlerWithConfig(cfg HandlerConfig) (http.Handler, error) {
	freshness := cfg.Freshness
	if freshness.Now == nil {
		freshness.Now = utcNow
	}
	if err := freshness.Validate(); err != nil {
		return nil, err
	}
	if cfg.Store == nil {
		return nil, errors.New("store is required")
	}

	keyPair := cfg.SigningKeyPair
	if !cfg.DisableSigningKey && len(keyPair.PublicKey) == 0 {
		loaded, err := spcrypto.LoadOrCreateKeyPair(cfg.SigningKeyPath)
		if err != nil {
			return nil, err
		}
		keyPair = loaded
	}
	rateLimits := normalizeRateLimitConfig(cfg.RateLimits)
	metrics := cfg.Metrics
	if metrics == nil {
		metrics = NewMetrics()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	startedAt := cfg.StartedAt
	if startedAt.IsZero() {
		startedAt = now().UTC()
	} else {
		startedAt = startedAt.UTC()
	}

	handler := &handler{
		store:               cfg.Store,
		freshness:           freshness,
		operatorAuth:        auth.NewOperatorTokenWithVerifier(cfg.OperatorToken, cfg.AllowUnauthenticatedOperatorAPI, cfg.Store),
		signingKey:          keyPair,
		secretKey:           append([]byte(nil), cfg.SecretKey...),
		events:              cfg.Events,
		eventTickets:        newEventTicketStore(),
		enrollmentLimiter:   newFixedWindowRateLimiter(rateLimits.enrollmentLimit, rateLimits.window, rateLimits.now),
		operatorAuthLimiter: newFixedWindowRateLimiter(rateLimits.operatorAuthLimit, rateLimits.window, rateLimits.now),
		metrics:             metrics,
		schemaVersion:       cfg.SchemaVersion,
		startedAt:           startedAt,
		now:                 now,
		timedOutJobs:        map[string]struct{}{},
		logger:              cfg.Logger,
	}
	if rateLimits.disableEnrollment {
		handler.enrollmentLimiter = nil
	}
	if rateLimits.disableOperatorAuth {
		handler.operatorAuthLimiter = nil
	}
	if handler.logger == nil {
		handler.logger = discardLogger()
	}
	handler.events = eventHubOrDefault(handler.events)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", jsonStatusHandler("ok"))
	mux.HandleFunc("/readyz", handler.readyz)
	mux.HandleFunc("/metrics", handler.metricsEndpoint)
	mux.HandleFunc("/api/events", handler.eventsStream)
	mux.HandleFunc("/api/events/tickets", handler.createEventTicket)
	mux.HandleFunc("/api/whoami", handler.whoami)
	mux.HandleFunc("/api/status", handler.serverStatus)
	mux.HandleFunc("/api/audit", handler.auditEvents)
	mux.HandleFunc("/api/audit/export", handler.exportAuditEvents)
	mux.HandleFunc("/api/operator-tokens", handler.operatorTokens)
	mux.HandleFunc("/api/operator-tokens/", handler.operatorTokenRouter)
	mux.HandleFunc("/api/webhooks", handler.alertWebhooks)
	mux.HandleFunc("/api/webhooks/", handler.alertWebhookRouter)
	mux.HandleFunc("/api/settings", handler.serverSettings)
	mux.HandleFunc("/api/signing-key", handler.publicSigningKey)
	mux.HandleFunc("/api/config/desired/history", handler.desiredConfigHistory)
	mux.HandleFunc("/api/config/desired/revert", handler.revertDesiredConfig)
	mux.HandleFunc("/api/config/desired", handler.desiredConfig)
	mux.HandleFunc("/api/config/providers", handler.configProviders)
	mux.HandleFunc("/api/config/effective", handler.effectiveConfig)
	mux.HandleFunc("/api/config/effective/preview", handler.previewEffectiveConfig)
	mux.HandleFunc("/api/enrollment-tokens", handler.createEnrollmentToken)
	mux.HandleFunc("/api/enroll", handler.enrollNode)
	mux.HandleFunc("/api/heartbeat", handler.heartbeat)
	mux.HandleFunc("/api/rollouts", handler.rollouts)
	mux.HandleFunc("/api/rollouts/", handler.rolloutRouter)
	mux.HandleFunc("/api/rollout-templates", handler.rolloutTemplates)
	mux.HandleFunc("/api/rollout-templates/", handler.rolloutTemplateRouter)
	mux.HandleFunc("/api/jobs/bulk", handler.bulkJobs)
	mux.HandleFunc("/api/nodes/labels", handler.bulkNodeLabels)
	mux.HandleFunc("/api/nodes", handler.nodes)
	mux.HandleFunc("/api/nodes/", handler.nodeJobsRouter)
	mux.HandleFunc("/api/sidecar/jobs/next", handler.claimNextJob)
	mux.HandleFunc("/api/sidecar/jobs/", handler.submitJobResult)
	return requestLogger(handler.logger, securityHeaders(mux)), nil
}

type handler struct {
	store               store.Store
	freshness           FreshnessPolicy
	operatorAuth        auth.OperatorToken
	signingKey          spcrypto.KeyPair
	secretKey           []byte
	events              *EventHub
	eventTickets        *eventTicketStore
	enrollmentLimiter   *fixedWindowRateLimiter
	operatorAuthLimiter *fixedWindowRateLimiter
	metrics             *Metrics
	schemaVersion       int
	startedAt           time.Time
	now                 func() time.Time
	timedOutMu          sync.Mutex
	timedOutJobs        map[string]struct{}
	logger              *slog.Logger
}

func jsonStatusHandler(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": status}); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
	}
}

func (h *handler) readyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if err := h.store.Check(r.Context()); err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "store is not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *handler) whoami(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	identity, ok := h.authorizeOperatorIdentity(w, r)
	if !ok {
		return
	}
	tokenName := "bootstrap"
	if identity.TokenID != "" {
		name, err := h.operatorTokenName(r.Context(), identity.TokenID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "resolve operator token")
			return
		}
		tokenName = name
	}
	writeJSON(w, http.StatusOK, protocol.WhoamiResponse{
		Scope:     identity.Scope,
		TokenName: tokenName,
	})
}

func (h *handler) serverStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if _, ok := h.authorizeOperatorIdentity(w, r); !ok {
		return
	}
	nodes, err := h.store.ListNodes(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "list nodes")
		return
	}
	rollouts, err := h.store.ListRollouts(r.Context(), store.RolloutFilter{Limit: 1})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "list rollouts")
		return
	}
	version, commit, buildDate := buildinfo.Labels()
	uptime := int64(h.now().UTC().Sub(h.startedAt).Seconds())
	if uptime < 0 {
		uptime = 0
	}
	writeJSON(w, http.StatusOK, protocol.ServerStatusResponse{
		Version:       version,
		Commit:        commit,
		BuildDate:     buildDate,
		UptimeSeconds: uptime,
		SchemaVersion: h.schemaVersion,
		NodeCount:     len(nodes),
		RolloutCount:  rollouts.Total,
	})
}

func (h *handler) operatorTokenName(ctx context.Context, tokenID string) (string, error) {
	tokens, err := h.store.ListOperatorTokens(ctx)
	if err != nil {
		return "", err
	}
	for _, token := range tokens {
		if token.ID == tokenID {
			return token.Name, nil
		}
	}
	return "unknown", nil
}

func (h *handler) metricsEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	fleetMetrics, err := h.collectFleetMetrics(r.Context())
	if err != nil {
		http.Error(w, "collect fleet metrics", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	h.metrics.WriteProm(w)
	writeFleetMetrics(w, fleetMetrics)
}

type fleetMetricsSnapshot struct {
	nodesByState         map[protocol.NodeState]int
	runtimeHealthByState map[labelPair]int
	rolloutsByState      map[string]int
	activeRollouts       int
	driftedNodes         int
	outdatedSidecars     int
	outdatedRuntimes     map[string]int
}

func (h *handler) collectFleetMetrics(ctx context.Context) (fleetMetricsSnapshot, error) {
	nodes, err := h.store.ListNodes(ctx)
	if err != nil {
		return fleetMetricsSnapshot{}, err
	}
	h.applyFreshness(nodes)

	desired, err := h.store.GetDesiredConfig(ctx)
	if err != nil {
		return fleetMetricsSnapshot{}, err
	}
	settings, err := h.store.GetServerSettings(ctx)
	if err != nil {
		return fleetMetricsSnapshot{}, err
	}

	snapshot := fleetMetricsSnapshot{
		nodesByState: map[protocol.NodeState]int{
			protocol.NodeStateFresh:   0,
			protocol.NodeStateStale:   0,
			protocol.NodeStateOffline: 0,
		},
		runtimeHealthByState: map[labelPair]int{},
		rolloutsByState:      newRolloutMetricStateCounts(),
		outdatedRuntimes:     newRuntimeOutdatedMetricCounts(),
	}
	for _, node := range nodes {
		snapshot.nodesByState[node.State]++
		for _, runtime := range node.Runtimes {
			runtimeType := strings.TrimSpace(runtime.Type)
			if runtimeType == "" {
				runtimeType = "unknown"
			}
			state := runtimeHealthStateLabel(runtime.Health.State)
			snapshot.runtimeHealthByState[labelPair{left: runtimeType, right: state}]++
			if runtimeOutdated(runtime, settings.ExpectedRuntimeVersions) {
				snapshot.outdatedRuntimes[runtimeType]++
			}
		}
		drift, err := h.nodeHasConfigDrift(ctx, node.NodeID, desired)
		if err != nil {
			return fleetMetricsSnapshot{}, err
		}
		if drift {
			snapshot.driftedNodes++
		}
		if sidecarOutdated(node, settings.ExpectedSidecarVersion) {
			snapshot.outdatedSidecars++
		}
	}
	if err := h.collectRolloutMetrics(ctx, &snapshot); err != nil {
		return fleetMetricsSnapshot{}, err
	}
	return snapshot, nil
}

func (h *handler) collectRolloutMetrics(ctx context.Context, snapshot *fleetMetricsSnapshot) error {
	offset := 0
	for {
		list, err := h.store.ListRollouts(ctx, store.RolloutFilter{Limit: store.MaxRolloutListLimit, Offset: offset})
		if err != nil {
			return err
		}
		for _, rollout := range list.Rollouts {
			state := rolloutMetricStateLabel(rollout.State)
			snapshot.rolloutsByState[state]++
			if rolloutMetricStateActive(state) {
				snapshot.activeRollouts++
			}
		}
		offset += len(list.Rollouts)
		if len(list.Rollouts) == 0 || offset >= list.Total {
			return nil
		}
	}
}

// sidecarOutdated reports whether a node's reported sidecar version differs from
// the operator-configured expected version. An empty expected version disables
// the check.
func sidecarOutdated(node protocol.NodeStatus, expectedVersion string) bool {
	expectedVersion = strings.TrimSpace(expectedVersion)
	if expectedVersion == "" {
		return false
	}
	return strings.TrimSpace(node.SidecarVersion) != expectedVersion
}

func runtimeOutdated(runtime protocol.RuntimeStatus, expectedVersions map[string]string) bool {
	runtimeType := strings.TrimSpace(runtime.Type)
	if runtimeType == "" {
		return false
	}
	actualVersion := strings.TrimSpace(runtime.Version)
	if actualVersion == "" {
		return false
	}
	expectedVersion := strings.TrimSpace(expectedVersions[runtimeType])
	if expectedVersion == "" {
		return false
	}
	return actualVersion != expectedVersion
}

func withRuntimeOutdatedFlags(runtimes []protocol.RuntimeStatus, expectedVersions map[string]string) []protocol.RuntimeStatus {
	if len(runtimes) == 0 {
		return nil
	}
	out := make([]protocol.RuntimeStatus, len(runtimes))
	for i, runtime := range runtimes {
		out[i] = runtime
		out[i].Outdated = runtimeOutdated(runtime, expectedVersions)
	}
	return out
}

func newRuntimeOutdatedMetricCounts() map[string]int {
	out := make(map[string]int, len(supportedExpectedRuntimeVersionTypes))
	for _, runtimeType := range supportedExpectedRuntimeVersionTypes {
		out[runtimeType] = 0
	}
	return out
}

func writeFleetMetrics(w http.ResponseWriter, snapshot fleetMetricsSnapshot) {
	fmt.Fprintln(w, "# HELP sideplane_fleet_nodes Nodes by freshness state.")
	fmt.Fprintln(w, "# TYPE sideplane_fleet_nodes gauge")
	for _, state := range []protocol.NodeState{protocol.NodeStateFresh, protocol.NodeStateStale, protocol.NodeStateOffline} {
		fmt.Fprintf(w, "sideplane_fleet_nodes{state=%q} %d\n", state, snapshot.nodesByState[state])
	}
	fmt.Fprintln(w, "# HELP sideplane_fleet_nodes_drifted Nodes with config drift.")
	fmt.Fprintln(w, "# TYPE sideplane_fleet_nodes_drifted gauge")
	fmt.Fprintf(w, "sideplane_fleet_nodes_drifted %d\n", snapshot.driftedNodes)
	fmt.Fprintln(w, "# HELP sideplane_fleet_sidecar_outdated Nodes running a sidecar version other than the expected version.")
	fmt.Fprintln(w, "# TYPE sideplane_fleet_sidecar_outdated gauge")
	fmt.Fprintf(w, "sideplane_fleet_sidecar_outdated %d\n", snapshot.outdatedSidecars)
	fmt.Fprintln(w, "# HELP sideplane_fleet_runtime_outdated Runtimes running a version other than the expected runtime version.")
	fmt.Fprintln(w, "# TYPE sideplane_fleet_runtime_outdated gauge")
	for _, runtimeType := range supportedExpectedRuntimeVersionTypes {
		fmt.Fprintf(w, "sideplane_fleet_runtime_outdated{runtime_type=%q} %d\n", runtimeType, snapshot.outdatedRuntimes[runtimeType])
	}
	fmt.Fprintln(w, "# HELP sideplane_runtime_health Runtime health states by runtime type.")
	fmt.Fprintln(w, "# TYPE sideplane_runtime_health gauge")
	if len(snapshot.runtimeHealthByState) == 0 {
		fmt.Fprintln(w, `sideplane_runtime_health{runtime_type="none",state="none"} 0`)
	} else {
		samples := make([]pairCounterSample, 0, len(snapshot.runtimeHealthByState))
		for labels, value := range snapshot.runtimeHealthByState {
			samples = append(samples, pairCounterSample{left: labels.left, right: labels.right, value: int64(value)})
		}
		sort.Slice(samples, func(i, j int) bool {
			if samples[i].left == samples[j].left {
				return samples[i].right < samples[j].right
			}
			return samples[i].left < samples[j].left
		})
		for _, sample := range samples {
			fmt.Fprintf(w, "sideplane_runtime_health{runtime_type=%q,state=%q} %d\n", sample.left, sample.right, sample.value)
		}
	}
	fmt.Fprintln(w, "# HELP sideplane_rollouts_active Active non-terminal rollouts.")
	fmt.Fprintln(w, "# TYPE sideplane_rollouts_active gauge")
	fmt.Fprintf(w, "sideplane_rollouts_active %d\n", snapshot.activeRollouts)
	fmt.Fprintln(w, "# HELP sideplane_rollouts Rollouts by lifecycle state.")
	fmt.Fprintln(w, "# TYPE sideplane_rollouts gauge")
	for _, state := range rolloutMetricStates {
		fmt.Fprintf(w, "sideplane_rollouts{state=%q} %d\n", state, snapshot.rolloutsByState[state])
	}
}

func runtimeHealthStateLabel(state protocol.RuntimeHealthState) string {
	switch state {
	case protocol.RuntimeHealthHealthy, protocol.RuntimeHealthDegraded, protocol.RuntimeHealthUnknown:
		return string(state)
	default:
		return string(protocol.RuntimeHealthUnknown)
	}
}

var rolloutMetricStates = []string{
	string(protocol.RolloutStatePending),
	string(protocol.RolloutStateScheduled),
	string(protocol.RolloutStateRunning),
	string(protocol.RolloutStatePaused),
	string(protocol.RolloutStateCompleted),
	string(protocol.RolloutStateFailed),
	string(protocol.RolloutStateAborted),
	"unknown",
}

func newRolloutMetricStateCounts() map[string]int {
	counts := make(map[string]int, len(rolloutMetricStates))
	for _, state := range rolloutMetricStates {
		counts[state] = 0
	}
	return counts
}

func rolloutMetricStateLabel(state protocol.RolloutState) string {
	switch state {
	case protocol.RolloutStatePending,
		protocol.RolloutStateScheduled,
		protocol.RolloutStateRunning,
		protocol.RolloutStatePaused,
		protocol.RolloutStateCompleted,
		protocol.RolloutStateFailed,
		protocol.RolloutStateAborted:
		return string(state)
	default:
		return "unknown"
	}
}

func rolloutMetricStateActive(state string) bool {
	switch state {
	case string(protocol.RolloutStatePending),
		string(protocol.RolloutStateScheduled),
		string(protocol.RolloutStateRunning),
		string(protocol.RolloutStatePaused):
		return true
	default:
		return false
	}
}

func (h *handler) createEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}

	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.CreateEnrollmentTokenRequest
	if err := decodeOptionalJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid enrollment token JSON")
		return
	}

	now := time.Now().UTC()
	expiresAt := req.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = now.Add(defaultEnrollmentTokenTTL)
	}
	if !expiresAt.After(now) {
		writeAPIError(w, http.StatusBadRequest, "expiresAt must be in the future")
		return
	}

	resp, err := h.store.CreateEnrollmentToken(r.Context(), expiresAt, now)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "create enrollment token")
		return
	}
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    audit.ActionEnrollmentTokenCreate,
		Detail:    "one-time enrollment token created",
		CreatedAt: now,
	})

	writeJSON(w, http.StatusCreated, resp)
}

func (h *handler) operatorTokens(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createOperatorToken(w, r)
	case http.MethodGet:
		h.listOperatorTokens(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
	}
}

func (h *handler) createOperatorToken(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.CreateOperatorTokenRequest
	if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid operator token JSON")
		return
	}
	name, err := store.ValidateOperatorTokenName(req.Name)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	scope, err := store.ValidateOperatorTokenScope(req.Scope)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	now := time.Now().UTC()
	resp, err := h.store.CreateOperatorToken(r.Context(), name, scope, now)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "create operator token")
		return
	}
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    audit.ActionOperatorTokenCreate,
		Detail:    fmt.Sprintf("operator token created id=%s name=%q scope=%s", resp.OperatorToken.ID, resp.OperatorToken.Name, resp.OperatorToken.Scope),
		CreatedAt: now,
	})
	writeJSON(w, http.StatusCreated, resp)
}

func (h *handler) listOperatorTokens(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeOperator(w, r) {
		return
	}
	tokens, err := h.store.ListOperatorTokens(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "list operator tokens")
		return
	}
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    audit.ActionOperatorTokenList,
		Detail:    fmt.Sprintf("operator token metadata listed count=%d", len(tokens)),
		CreatedAt: time.Now().UTC(),
	})
	writeJSON(w, http.StatusOK, protocol.ListOperatorTokensResponse{Tokens: tokens})
}

func (h *handler) operatorTokenRouter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", http.MethodDelete)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if !h.authorizeOperator(w, r) {
		return
	}
	tokenID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/operator-tokens/"))
	if tokenID == "" || strings.Contains(tokenID, "/") {
		writeAPIError(w, http.StatusNotFound, "operator token not found")
		return
	}

	now := time.Now().UTC()
	token, err := h.store.RevokeOperatorToken(r.Context(), tokenID, now)
	if errors.Is(err, store.ErrOperatorTokenNotFound) {
		writeAPIError(w, http.StatusNotFound, "operator token not found")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "revoke operator token")
		return
	}
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    audit.ActionOperatorTokenRevoke,
		Detail:    fmt.Sprintf("operator token revoked id=%s name=%q", token.ID, token.Name),
		CreatedAt: now,
	})
	writeJSON(w, http.StatusOK, protocol.RevokeOperatorTokenResponse{OperatorToken: token})
}

func (h *handler) serverSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := h.store.GetServerSettings(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "get server settings")
			return
		}
		writeJSON(w, http.StatusOK, normalizeServerSettings(settings))
	case http.MethodPut:
		if !h.authorizeOperator(w, r) {
			return
		}
		var req protocol.UpdateServerSettingsRequest
		if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
			writeJSONDecodeError(w, err, "invalid settings JSON")
			return
		}
		version := strings.TrimSpace(req.ExpectedSidecarVersion)
		if len(version) > 200 {
			writeAPIError(w, http.StatusBadRequest, "expectedSidecarVersion is too long")
			return
		}
		settings, err := h.store.GetServerSettings(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "get server settings")
			return
		}
		settings.ExpectedSidecarVersion = version
		runtimeVersionsUpdated := req.ExpectedRuntimeVersions != nil
		if runtimeVersionsUpdated {
			expectedRuntimeVersions, err := normalizeExpectedRuntimeVersions(req.ExpectedRuntimeVersions)
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, err.Error())
				return
			}
			settings.ExpectedRuntimeVersions = expectedRuntimeVersions
		}
		if err := h.store.SetExpectedSidecarVersion(r.Context(), version); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "update server settings")
			return
		}
		if runtimeVersionsUpdated {
			if err := h.store.SetExpectedRuntimeVersions(r.Context(), settings.ExpectedRuntimeVersions); err != nil {
				writeAPIError(w, http.StatusInternalServerError, "update server settings")
				return
			}
		}
		h.audit(r.Context(), protocol.AuditEvent{
			Actor:     audit.ActorOperator,
			Action:    audit.ActionServerSettingsUpdate,
			Detail:    serverSettingsAuditDetail(settings),
			CreatedAt: time.Now().UTC(),
		})
		writeJSON(w, http.StatusOK, normalizeServerSettings(settings))
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
	}
}

func normalizeServerSettings(settings protocol.ServerSettings) protocol.ServerSettings {
	runtimeVersions, _ := normalizeExpectedRuntimeVersions(settings.ExpectedRuntimeVersions)
	return protocol.ServerSettings{
		ExpectedSidecarVersion:  strings.TrimSpace(settings.ExpectedSidecarVersion),
		ExpectedRuntimeVersions: runtimeVersions,
	}
}

func normalizeExpectedRuntimeVersions(input map[string]string) (map[string]string, error) {
	out := map[string]string{}
	allowed := map[string]struct{}{}
	for _, runtimeType := range supportedExpectedRuntimeVersionTypes {
		allowed[runtimeType] = struct{}{}
	}
	for runtimeType, version := range input {
		runtimeType = strings.TrimSpace(runtimeType)
		version = strings.TrimSpace(version)
		if runtimeType == "" {
			continue
		}
		if _, ok := allowed[runtimeType]; !ok {
			return nil, fmt.Errorf("unsupported expected runtime version type %q", runtimeType)
		}
		if len(version) > 200 {
			return nil, fmt.Errorf("expectedRuntimeVersions.%s is too long", runtimeType)
		}
		if version == "" {
			continue
		}
		out[runtimeType] = version
	}
	return out, nil
}

func serverSettingsAuditDetail(settings protocol.ServerSettings) string {
	parts := []string{fmt.Sprintf("expectedSidecarVersion=%q", strings.TrimSpace(settings.ExpectedSidecarVersion))}
	for _, runtimeType := range supportedExpectedRuntimeVersionTypes {
		if version := strings.TrimSpace(settings.ExpectedRuntimeVersions[runtimeType]); version != "" {
			parts = append(parts, fmt.Sprintf("expectedRuntimeVersions.%s=%q", runtimeType, version))
		}
	}
	return strings.Join(parts, " ")
}

func (h *handler) alertWebhooks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createAlertWebhook(w, r)
	case http.MethodGet:
		h.listAlertWebhooks(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
	}
}

func (h *handler) createAlertWebhook(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.CreateAlertWebhookRequest
	if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid webhook JSON")
		return
	}
	req.Secret = strings.TrimSpace(req.Secret)
	if req.Secret == "" && req.Sign {
		secret, err := newAlertWebhookSecret()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "generate webhook secret")
			return
		}
		req.Secret = secret
	}

	now := time.Now().UTC()
	webhook, err := h.store.CreateAlertWebhook(r.Context(), req, now)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    audit.ActionAlertWebhookCreate,
		Detail:    fmt.Sprintf("alert webhook created id=%s kind=%s url=%s events=%d signed=%t", webhook.ID, webhook.Kind, webhook.URL, len(webhook.Events), webhook.HasSecret),
		CreatedAt: now,
	})
	// The signing secret is returned exactly once, here at creation time.
	writeJSON(w, http.StatusCreated, protocol.CreateAlertWebhookResponse{Webhook: webhook, Secret: req.Secret})
}

func (h *handler) listAlertWebhooks(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeOperator(w, r) {
		return
	}
	webhooks, err := h.store.ListAlertWebhooks(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "list webhooks")
		return
	}
	writeJSON(w, http.StatusOK, protocol.ListAlertWebhooksResponse{Webhooks: webhooks})
}

func (h *handler) alertWebhookRouter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", http.MethodDelete)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if !h.authorizeOperator(w, r) {
		return
	}
	webhookID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/webhooks/"))
	if webhookID == "" || strings.Contains(webhookID, "/") {
		writeAPIError(w, http.StatusNotFound, "alert webhook not found")
		return
	}
	if err := h.store.DeleteAlertWebhook(r.Context(), webhookID); err != nil {
		if errors.Is(err, store.ErrAlertWebhookNotFound) {
			writeAPIError(w, http.StatusNotFound, "alert webhook not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "delete webhook")
		return
	}
	now := time.Now().UTC()
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    audit.ActionAlertWebhookDelete,
		Detail:    "alert webhook deleted id=" + webhookID,
		CreatedAt: now,
	})
	w.WriteHeader(http.StatusNoContent)
}

func newAlertWebhookSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (h *handler) enrollNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if ok, retryAfter := h.enrollmentLimiter.allow(remoteRateLimitKey(r)); !ok {
		writeRateLimited(w, retryAfter)
		return
	}

	var req protocol.EnrollNodeRequest
	if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid enroll JSON")
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		writeAPIError(w, http.StatusBadRequest, "token is required")
		return
	}

	resp, err := h.store.EnrollNode(r.Context(), req, time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, store.ErrEnrollmentTokenInvalid),
			errors.Is(err, store.ErrEnrollmentTokenExpired),
			errors.Is(err, store.ErrEnrollmentTokenUsed):
			writeAPIError(w, http.StatusUnauthorized, "enrollment token rejected")
		case errors.Is(err, store.ErrNodeAlreadyEnrolled):
			writeAPIError(w, http.StatusConflict, "node is already enrolled")
		default:
			writeAPIError(w, http.StatusInternalServerError, "enroll node")
		}
		return
	}
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:      audit.ActorNode,
		Action:     audit.ActionNodeEnroll,
		TargetNode: resp.NodeID,
		Detail:     "node enrolled",
		CreatedAt:  time.Now().UTC(),
	})

	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) heartbeat(w http.ResponseWriter, r *http.Request) {
	heartbeatAccepted := false
	defer func() {
		if heartbeatAccepted {
			h.metrics.IncHeartbeat("accepted")
		} else {
			h.metrics.IncHeartbeat("rejected")
		}
	}()

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}

	var req protocol.HeartbeatRequest
	if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid heartbeat JSON")
		return
	}

	req.NodeID = strings.TrimSpace(req.NodeID)
	if req.NodeID == "" {
		writeAPIError(w, http.StatusBadRequest, "nodeId is required")
		return
	}

	credential, ok := auth.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized))
		return
	}

	ok, err := h.store.VerifyNodeCredential(r.Context(), req.NodeID, credential)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "verify node credential")
		return
	}
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized))
		return
	}

	now := time.Now().UTC()
	node, err := h.store.RecordHeartbeat(r.Context(), req, now)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "record heartbeat")
		return
	}
	heartbeatAccepted = true
	node.State = h.freshness.StateFor(node.LastHeartbeatAt)
	publishNodeEvent(h.events, node)

	writeJSON(w, http.StatusOK, protocol.HeartbeatResponse{
		Accepted:   true,
		ServerTime: now,
		Node:       node,
	})
}

func (h *handler) nodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if !h.authorizeOperatorRead(w, r) {
		return
	}

	filter, err := parseNodeFilter(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	nodeList, err := h.store.ListNodesFiltered(r.Context(), filter)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "list nodes")
		return
	}
	h.applyFreshness(nodeList.Nodes)

	desired, err := h.store.GetDesiredConfig(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "get desired config")
		return
	}
	settings, err := h.store.GetServerSettings(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "get server settings")
		return
	}

	response := make([]protocol.NodeStatusWithDrift, len(nodeList.Nodes))
	for i, node := range nodeList.Nodes {
		drift, err := h.nodeHasConfigDrift(r.Context(), node.NodeID, desired)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "get actual config")
			return
		}
		node.Runtimes = withRuntimeOutdatedFlags(node.Runtimes, settings.ExpectedRuntimeVersions)
		response[i] = protocol.NodeStatusWithDrift{
			NodeStatus:      node,
			Drift:           drift,
			SidecarOutdated: sidecarOutdated(node, settings.ExpectedSidecarVersion),
		}
	}

	writeJSON(w, http.StatusOK, protocol.ListNodesResponse{
		Nodes:  response,
		Total:  nodeList.Total,
		Limit:  nodeList.Limit,
		Offset: nodeList.Offset,
	})
}

func (h *handler) rollouts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createRollout(w, r)
	case http.MethodGet:
		h.listRollouts(w, r)
	default:
		w.Header().Set("Allow", http.MethodPost+", "+http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
	}
}

func (h *handler) rolloutRouter(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/rollouts/")
	parts := strings.Split(path, "/")
	rolloutID := strings.TrimSpace(parts[0])
	if rolloutID == "" {
		writeAPIError(w, http.StatusNotFound, http.StatusText(http.StatusNotFound))
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
			return
		}
		h.getRollout(w, r, rolloutID)
		return
	}
	if len(parts) == 2 && parts[1] == "actions" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
			return
		}
		h.rolloutAction(w, r, rolloutID)
		return
	}
	writeAPIError(w, http.StatusNotFound, http.StatusText(http.StatusNotFound))
}

func (h *handler) createRollout(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.CreateRolloutRequest
	if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid rollout JSON")
		return
	}
	baseSpec := req.Spec
	if templateID := strings.TrimSpace(req.TemplateID); templateID != "" {
		template, err := h.store.GetRolloutTemplate(r.Context(), templateID)
		if err != nil {
			if errors.Is(err, store.ErrRolloutTemplateNotFound) {
				writeAPIError(w, http.StatusNotFound, "rollout template not found")
				return
			}
			writeAPIError(w, http.StatusInternalServerError, "load rollout template")
			return
		}
		// The template prefills the spec; it is still normalized, validated, and
		// resolved below at creation time.
		baseSpec = template.Spec
	}
	spec, err := normalizeRolloutSpec(baseSpec)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRolloutSpec(spec); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	nodeIDs, err := h.resolveRolloutNodes(r.Context(), spec)
	if err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			writeAPIError(w, http.StatusNotFound, "node not found")
			return
		}
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(nodeIDs) == 0 {
		writeAPIError(w, http.StatusBadRequest, "rollout target set is empty")
		return
	}
	if !spec.AllowOverlap {
		conflicts, err := h.store.ListActiveRolloutConflicts(r.Context(), nodeIDs)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "check rollout overlap")
			return
		}
		if len(conflicts) > 0 {
			writeAPIError(w, http.StatusConflict, rolloutOverlapConflictMessage(conflicts))
			return
		}
	}
	spec.NodeIDs = nodeIDs
	spec.Selector = cloneStringMap(spec.Selector)
	now := time.Now().UTC()
	state := protocol.RolloutStatePending
	if rolloutStartsInFuture(spec, now) {
		state = protocol.RolloutStateScheduled
	}
	created, err := h.store.CreateRollout(r.Context(), protocol.Rollout{
		Spec:      spec,
		State:     state,
		Batches:   rolloutengine.PlanBatches(nodeIDs, spec.BatchSize),
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "create rollout")
		return
	}
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    audit.ActionRolloutCreate,
		Detail:    fmt.Sprintf("rollout=%s nodes=%d runtime=%s live=%t autoRollback=%t", created.ID, len(nodeIDs), spec.RuntimeType, spec.Live, spec.AutoRollbackOnFailure),
		CreatedAt: now,
	})
	publishRolloutEventWithActor(h.events, created, h.operatorActorName(r.Context()))
	writeJSON(w, http.StatusCreated, protocol.CreateRolloutResponse{Rollout: created})
}

func rolloutOverlapConflictMessage(conflicts []store.RolloutNodeConflict) string {
	parts := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		state := strings.TrimSpace(string(conflict.State))
		if state == "" {
			state = "unknown"
		}
		parts = append(parts, fmt.Sprintf("%s in rollout %s (%s)", conflict.NodeID, conflict.RolloutID, state))
	}
	return "rollout overlaps active target(s): " + strings.Join(parts, "; ") + "; set allowOverlap=true to override"
}

func (h *handler) rolloutTemplates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createRolloutTemplate(w, r)
	case http.MethodGet:
		h.listRolloutTemplates(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
	}
}

func (h *handler) createRolloutTemplate(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.CreateRolloutTemplateRequest
	if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid rollout template JSON")
		return
	}
	name, err := store.ValidateRolloutTemplateName(req.Name)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	spec, err := normalizeRolloutSpec(req.Spec)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRolloutSpec(spec); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	now := time.Now().UTC()
	template, err := h.store.CreateRolloutTemplate(r.Context(), name, spec, now)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    audit.ActionRolloutTemplateCreate,
		Detail:    fmt.Sprintf("rollout template created id=%s name=%q runtime=%s", template.ID, template.Name, spec.RuntimeType),
		CreatedAt: now,
	})
	writeJSON(w, http.StatusCreated, protocol.CreateRolloutTemplateResponse{Template: template})
}

func (h *handler) listRolloutTemplates(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeOperator(w, r) {
		return
	}
	templates, err := h.store.ListRolloutTemplates(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "list rollout templates")
		return
	}
	writeJSON(w, http.StatusOK, protocol.ListRolloutTemplatesResponse{Templates: templates})
}

func (h *handler) rolloutTemplateRouter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", http.MethodDelete)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if !h.authorizeOperator(w, r) {
		return
	}
	templateID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/rollout-templates/"))
	if templateID == "" || strings.Contains(templateID, "/") {
		writeAPIError(w, http.StatusNotFound, "rollout template not found")
		return
	}
	if err := h.store.DeleteRolloutTemplate(r.Context(), templateID); err != nil {
		if errors.Is(err, store.ErrRolloutTemplateNotFound) {
			writeAPIError(w, http.StatusNotFound, "rollout template not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "delete rollout template")
		return
	}
	now := time.Now().UTC()
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    audit.ActionRolloutTemplateDelete,
		Detail:    "rollout template deleted id=" + templateID,
		CreatedAt: now,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) listRollouts(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeOperator(w, r) {
		return
	}
	filter, err := parseRolloutFilter(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := h.store.ListRollouts(r.Context(), filter)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "list rollouts")
		return
	}
	writeJSON(w, http.StatusOK, protocol.ListRolloutsResponse{
		Rollouts: list.Rollouts,
		Total:    list.Total,
		Limit:    list.Limit,
		Offset:   list.Offset,
	})
}

func (h *handler) getRollout(w http.ResponseWriter, r *http.Request, rolloutID string) {
	if !h.authorizeOperator(w, r) {
		return
	}
	rollout, err := h.store.GetRollout(r.Context(), rolloutID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "get rollout")
		return
	}
	if rollout == nil {
		writeAPIError(w, http.StatusNotFound, "rollout not found")
		return
	}
	writeJSON(w, http.StatusOK, protocol.GetRolloutResponse{Rollout: *rollout})
}

func (h *handler) rolloutAction(w http.ResponseWriter, r *http.Request, rolloutID string) {
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.RolloutActionRequest
	if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid rollout action JSON")
		return
	}
	rollout, err := h.store.GetRollout(r.Context(), rolloutID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "get rollout")
		return
	}
	if rollout == nil {
		writeAPIError(w, http.StatusNotFound, "rollout not found")
		return
	}
	now := time.Now().UTC()
	previousState := rollout.State
	action := ""
	switch req.Action {
	case protocol.RolloutActionPause:
		*rollout = pauseRolloutForOperator(*rollout, now)
		action = audit.ActionRolloutPause
	case protocol.RolloutActionResume:
		*rollout = rolloutengine.Resume(*rollout, now)
		action = audit.ActionRolloutResume
	case protocol.RolloutActionAbort:
		*rollout = rolloutengine.Abort(*rollout, now)
		action = audit.ActionRolloutAbort
	default:
		writeAPIError(w, http.StatusBadRequest, "unsupported rollout action")
		return
	}
	if err := h.store.UpdateRollout(r.Context(), *rollout); err != nil {
		if errors.Is(err, store.ErrRolloutNotFound) {
			writeAPIError(w, http.StatusNotFound, "rollout not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "update rollout")
		return
	}
	if previousState != rollout.State {
		h.metrics.IncRolloutTerminal(string(rollout.State))
	}
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    action,
		Detail:    "rollout=" + rollout.ID,
		CreatedAt: now,
	})
	publishRolloutEventWithActor(h.events, *rollout, h.operatorActorName(r.Context()))
	writeJSON(w, http.StatusOK, protocol.RolloutActionResponse{Rollout: *rollout})
}

func normalizeRolloutSpec(spec protocol.RolloutSpec) (protocol.RolloutSpec, error) {
	spec.RuntimeType = strings.TrimSpace(spec.RuntimeType)
	spec.Profile = strings.TrimSpace(spec.Profile)
	if spec.BatchSize <= 0 {
		spec.BatchSize = 1
	}
	if spec.HealthTimeout <= 0 {
		spec.HealthTimeout = rolloutengine.DefaultHealthTimeout
	}
	spec.NodeIDs = uniqueTrimmedStrings(spec.NodeIDs)
	selector, err := store.ValidateNodeLabels(spec.Selector)
	if err != nil {
		return protocol.RolloutSpec{}, fmt.Errorf("invalid selector: %w", err)
	}
	spec.Selector = selector
	spec.Target.Provider = strings.TrimSpace(spec.Target.Provider)
	spec.Target.Model = strings.TrimSpace(spec.Target.Model)
	if !spec.StartAt.IsZero() {
		spec.StartAt = spec.StartAt.UTC()
	}
	return spec, nil
}

func validateRolloutSpec(spec protocol.RolloutSpec) error {
	if len(spec.NodeIDs) > 0 && len(spec.Selector) > 0 {
		return fmt.Errorf("selector and nodeIds are mutually exclusive")
	}
	if err := rolloutengine.ValidateRolloutSpec(spec); err != nil {
		return err
	}
	if spec.RuntimeType != "hermes" && spec.RuntimeType != "openclaw" {
		return fmt.Errorf("unsupported runtime type")
	}
	if err := spconfig.ValidateProviderModelSelection(spec.Target); err != nil {
		return fmt.Errorf("invalid target provider/model: %w", err)
	}
	if err := validateRolloutStartAt(spec.StartAt, time.Now().UTC()); err != nil {
		return err
	}
	return nil
}

func validateRolloutStartAt(startAt time.Time, now time.Time) error {
	if startAt.IsZero() {
		return nil
	}
	startAt = startAt.UTC()
	if startAt.Before(now.Add(-rolloutStartAtPastLimit)) {
		return fmt.Errorf("startAt is too far in the past")
	}
	if startAt.After(now.Add(rolloutStartAtFutureLimit)) {
		return fmt.Errorf("startAt is too far in the future")
	}
	return nil
}

func rolloutStartsInFuture(spec protocol.RolloutSpec, now time.Time) bool {
	return !spec.StartAt.IsZero() && now.Before(spec.StartAt.UTC())
}

func (h *handler) resolveRolloutNodes(ctx context.Context, spec protocol.RolloutSpec) ([]string, error) {
	return h.resolveTargetNodes(ctx, spec.Selector, spec.NodeIDs, spec.IncludeMaintenance)
}

// resolveTargetNodes resolves an operator node selection to node IDs. When
// nodeIDs is set, each must exist; otherwise the selector matches labels.
func (h *handler) resolveTargetNodes(ctx context.Context, selector map[string]string, nodeIDs []string, includeMaintenance bool) ([]string, error) {
	if len(nodeIDs) > 0 {
		nodes, err := h.store.ListNodes(ctx)
		if err != nil {
			return nil, err
		}
		byID := make(map[string]protocol.NodeStatus, len(nodes))
		for _, node := range nodes {
			byID[node.NodeID] = node
		}
		resolved := make([]string, 0, len(nodeIDs))
		for _, nodeID := range nodeIDs {
			node, ok := byID[nodeID]
			if !ok {
				return nil, store.ErrNodeNotFound
			}
			if node.Maintenance && !includeMaintenance {
				continue
			}
			resolved = append(resolved, nodeID)
		}
		return resolved, nil
	}
	list, err := h.store.ListNodesFiltered(ctx, store.NodeFilter{
		Labels: selector,
		Limit:  store.MaxNodeListLimit,
	})
	if err != nil {
		return nil, err
	}
	resolved := make([]string, 0, len(list.Nodes))
	for _, node := range list.Nodes {
		if node.Maintenance && !includeMaintenance {
			continue
		}
		resolved = append(resolved, node.NodeID)
	}
	return resolved, nil
}

func parseRolloutFilter(r *http.Request) (store.RolloutFilter, error) {
	filter := store.RolloutFilter{Limit: store.DefaultRolloutListLimit}
	query := r.URL.Query()
	if limitValue := strings.TrimSpace(query.Get("limit")); limitValue != "" {
		limit, err := strconv.Atoi(limitValue)
		if err != nil || limit <= 0 {
			return store.RolloutFilter{}, fmt.Errorf("limit must be a positive integer")
		}
		if limit > store.MaxRolloutListLimit {
			limit = store.MaxRolloutListLimit
		}
		filter.Limit = limit
	}
	if offsetValue := strings.TrimSpace(query.Get("offset")); offsetValue != "" {
		offset, err := strconv.Atoi(offsetValue)
		if err != nil || offset < 0 {
			return store.RolloutFilter{}, fmt.Errorf("offset must be a non-negative integer")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func parseDesiredConfigHistoryFilter(r *http.Request) (store.DesiredConfigHistoryFilter, error) {
	filter := store.DesiredConfigHistoryFilter{Limit: store.DefaultDesiredConfigHistoryListLimit}
	query := r.URL.Query()
	if limitValue := strings.TrimSpace(query.Get("limit")); limitValue != "" {
		limit, err := strconv.Atoi(limitValue)
		if err != nil || limit <= 0 {
			return store.DesiredConfigHistoryFilter{}, fmt.Errorf("limit must be a positive integer")
		}
		if limit > store.MaxDesiredConfigHistoryListLimit {
			limit = store.MaxDesiredConfigHistoryListLimit
		}
		filter.Limit = limit
	}
	if offsetValue := strings.TrimSpace(query.Get("offset")); offsetValue != "" {
		offset, err := strconv.Atoi(offsetValue)
		if err != nil || offset < 0 {
			return store.DesiredConfigHistoryFilter{}, fmt.Errorf("offset must be a non-negative integer")
		}
		filter.Offset = offset
	}
	return filter, nil
}

func (h *handler) findDesiredConfigHistoryEntry(ctx context.Context, historyID string) (protocol.DesiredConfigHistoryEntry, bool, error) {
	offset := 0
	for {
		page, err := h.store.ListDesiredConfigHistory(ctx, store.DesiredConfigHistoryFilter{
			Limit:  store.MaxDesiredConfigHistoryListLimit,
			Offset: offset,
		})
		if err != nil {
			return protocol.DesiredConfigHistoryEntry{}, false, err
		}
		for _, entry := range page.History {
			if entry.ID == historyID {
				return entry, true, nil
			}
		}
		offset += len(page.History)
		if len(page.History) == 0 || offset >= page.Total {
			return protocol.DesiredConfigHistoryEntry{}, false, nil
		}
	}
}

func pauseRolloutForOperator(rollout protocol.Rollout, now time.Time) protocol.Rollout {
	if rollout.State == protocol.RolloutStateCompleted || rollout.State == protocol.RolloutStateAborted || rollout.State == protocol.RolloutStateFailed {
		return rollout
	}
	rollout.State = protocol.RolloutStatePaused
	rollout.PauseReason = "operator paused"
	rollout.UpdatedAt = now.UTC()
	for i := range rollout.Batches {
		if rollout.Batches[i].State == protocol.RolloutBatchStateRunning || rollout.Batches[i].State == protocol.RolloutBatchStatePending {
			rollout.Batches[i].State = protocol.RolloutBatchStatePaused
			break
		}
	}
	return rollout
}

func uniqueTrimmedStrings(values []string) []string {
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

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func parseNodeFilter(r *http.Request) (store.NodeFilter, error) {
	filter := store.NodeFilter{Limit: store.DefaultNodeListLimit}
	query := r.URL.Query()
	if limitValue := strings.TrimSpace(query.Get("limit")); limitValue != "" {
		limit, err := strconv.Atoi(limitValue)
		if err != nil || limit <= 0 {
			return store.NodeFilter{}, fmt.Errorf("limit must be a positive integer")
		}
		if limit > store.MaxNodeListLimit {
			limit = store.MaxNodeListLimit
		}
		filter.Limit = limit
	}
	if offsetValue := strings.TrimSpace(query.Get("offset")); offsetValue != "" {
		offset, err := strconv.Atoi(offsetValue)
		if err != nil || offset < 0 {
			return store.NodeFilter{}, fmt.Errorf("offset must be a non-negative integer")
		}
		filter.Offset = offset
	}
	if selectorValue := strings.TrimSpace(query.Get("selector")); selectorValue != "" {
		labels, err := parseLabelSelector(selectorValue)
		if err != nil {
			return store.NodeFilter{}, err
		}
		filter.Labels = labels
	}
	return filter, nil
}

func parseLabelSelector(selector string) (map[string]string, error) {
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
		if !ok {
			return nil, fmt.Errorf("selector entries must use key=value")
		}
		key = strings.TrimSpace(key)
		if _, exists := labels[key]; exists {
			return nil, fmt.Errorf("selector contains duplicate key %q", key)
		}
		labels[key] = strings.TrimSpace(value)
	}
	normalized, err := store.ValidateNodeLabels(labels)
	if err != nil {
		return nil, fmt.Errorf("invalid selector: %w", err)
	}
	return normalized, nil
}

func (h *handler) applyFreshness(nodes []protocol.NodeStatus) {
	for i := range nodes {
		nodes[i].State = h.freshness.StateFor(nodes[i].LastHeartbeatAt)
	}
}

// nodeHasConfigDrift reports whether any of the node's runtimes is running a
// provider/model that differs from its effective desired config. Drift is
// evaluated PER RUNTIME (using each snapshot's runtime type and profile) so that
// runtime/profile-scoped desired overrides are honored — comparing the node's
// actual hermes config against the unscoped global desired would otherwise
// report false drift whenever a per-runtime override differs from the global.
// Only runtimes whose actual AND desired provider+model are both known count.
func (h *handler) nodeHasConfigDrift(ctx context.Context, nodeID string, desired protocol.DesiredConfig) (bool, error) {
	snapshots, err := h.latestActualSnapshots(ctx, nodeID)
	if err != nil {
		return false, err
	}
	for i := range snapshots {
		actual := snapshots[i]
		if !hasKnownProviderModel(protocol.ProviderModelConfig{Provider: actual.Provider, Model: actual.Model}) {
			continue
		}
		profile := strings.TrimSpace(actual.Profile)
		if profile == "" {
			profile = "default"
		}
		effective := spconfig.EffectiveProviderModelConfig(desired, spconfig.EffectiveConfigTarget{
			NodeID:      nodeID,
			RuntimeType: strings.TrimSpace(actual.RuntimeType),
			Profile:     profile,
		})
		if !hasKnownProviderModel(effective) {
			continue
		}
		for _, entry := range spconfig.DiffProviderModelConfig(&actual, effective) {
			if entry.Change == protocol.ConfigDiffChangeUpdate &&
				strings.TrimSpace(entry.Actual) != "" &&
				strings.TrimSpace(entry.Desired) != "" {
				return true, nil
			}
		}
	}
	return false, nil
}

// latestActualSnapshots returns all runtime config snapshots from the node's
// most recent completed deep probe that produced any snapshots.
func (h *handler) latestActualSnapshots(ctx context.Context, nodeID string) ([]protocol.RuntimeConfigSnapshot, error) {
	jobs, err := h.store.ListNodeJobs(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if job.Type != protocol.JobTypeDeepProbe || job.Status != protocol.JobStatusCompleted || strings.TrimSpace(job.ResultJSON) == "" {
			continue
		}
		var result protocol.DeepProbeResult
		if err := json.Unmarshal([]byte(job.ResultJSON), &result); err != nil {
			continue
		}
		if len(result.ConfigSnapshots) > 0 {
			return result.ConfigSnapshots, nil
		}
	}
	return nil, nil
}

func hasKnownProviderModel(value protocol.ProviderModelConfig) bool {
	return strings.TrimSpace(value.Provider) != "" && strings.TrimSpace(value.Model) != ""
}

// nodeJobsRouter handles node-scoped API routes under /api/nodes/{nodeId}.
func (h *handler) nodeJobsRouter(w http.ResponseWriter, r *http.Request) {
	// Parse path: /api/nodes/{nodeId}/{labels|maintenance|backups|jobs|config-apply|restart|rollback}
	path := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	parts := strings.Split(path, "/")
	nodeID := strings.TrimSpace(parts[0])
	if nodeID == "" {
		writeAPIError(w, http.StatusNotFound, http.StatusText(http.StatusNotFound))
		return
	}

	if len(parts) == 1 {
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", http.MethodDelete)
			writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
			return
		}
		h.deleteNode(w, r, nodeID)
		return
	}
	if len(parts) != 2 {
		writeAPIError(w, http.StatusNotFound, http.StatusText(http.StatusNotFound))
		return
	}

	switch parts[1] {
	case "labels":
		switch r.Method {
		case http.MethodGet:
			h.getNodeLabels(w, r, nodeID)
		case http.MethodPut:
			h.setNodeLabels(w, r, nodeID)
		default:
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodPut)
			writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		}
	case "maintenance":
		if r.Method != http.MethodPut {
			w.Header().Set("Allow", http.MethodPut)
			writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
			return
		}
		h.setNodeMaintenance(w, r, nodeID)
	case "backups":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
			return
		}
		h.listNodeBackups(w, r, nodeID)
	case "jobs":
		switch r.Method {
		case http.MethodGet:
			h.listNodeJobs(w, r, nodeID)
		case http.MethodPost:
			h.createNodeJob(w, r, nodeID)
		default:
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
			writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		}
	case "config-apply":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
			return
		}
		h.createConfigApplyJob(w, r, nodeID)
	case "restart":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
			return
		}
		h.createRestartJob(w, r, nodeID)
	case "rollback":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
			return
		}
		h.createRollbackJob(w, r, nodeID)
	default:
		writeAPIError(w, http.StatusNotFound, http.StatusText(http.StatusNotFound))
	}
}

func (h *handler) listNodeBackups(w http.ResponseWriter, r *http.Request, nodeID string) {
	if !h.authorizeOperator(w, r) {
		return
	}
	limit, err := parseBackupLimit(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	exists, err := h.store.NodeExists(r.Context(), nodeID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "lookup node")
		return
	}
	if !exists {
		writeAPIError(w, http.StatusNotFound, "node not found")
		return
	}

	jobs, err := h.store.ListNodeJobsFiltered(r.Context(), nodeID, store.JobFilter{Limit: store.MaxJobListLimit})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "list node jobs")
		return
	}
	backups := store.ListRollbackBackups(jobs)
	total := len(backups)
	if len(backups) > limit {
		backups = backups[:limit]
	}
	response := make([]protocol.RollbackBackupInventoryItem, len(backups))
	for i, backup := range backups {
		response[i] = protocol.RollbackBackupInventoryItem{
			Ref:         backup.Ref,
			SourceJobID: backup.SourceJobID,
			RuntimeType: backup.RuntimeType,
			Profile:     backup.Profile,
			ConfigHash:  backup.ConfigHash,
			CreatedAt:   backup.CreatedAt,
		}
	}
	writeJSON(w, http.StatusOK, protocol.ListRollbackBackupsResponse{
		Backups: response,
		Total:   total,
		Limit:   limit,
	})
}

func parseBackupLimit(r *http.Request) (int, error) {
	limit := defaultBackupListLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, fmt.Errorf("limit must be a positive integer")
		}
		limit = parsed
	}
	if limit > maxBackupListLimit {
		limit = maxBackupListLimit
	}
	return limit, nil
}

func (h *handler) getNodeLabels(w http.ResponseWriter, r *http.Request, nodeID string) {
	if !h.authorizeOperator(w, r) {
		return
	}
	labels, err := h.store.GetNodeLabels(r.Context(), nodeID)
	if err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			writeAPIError(w, http.StatusNotFound, "node not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "get node labels")
		return
	}
	if labels == nil {
		labels = map[string]string{}
	}
	writeJSON(w, http.StatusOK, protocol.NodeLabelsResponse{
		NodeID: nodeID,
		Labels: labels,
	})
}

func (h *handler) setNodeLabels(w http.ResponseWriter, r *http.Request, nodeID string) {
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.NodeLabelsRequest
	if err := decodeOptionalJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid node labels JSON")
		return
	}
	labels, err := store.ValidateNodeLabels(req.Labels)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.SetNodeLabels(r.Context(), nodeID, labels); err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			writeAPIError(w, http.StatusNotFound, "node not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "set node labels")
		return
	}
	if labels == nil {
		labels = map[string]string{}
	}
	now := time.Now().UTC()
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:      audit.ActorOperator,
		Action:     audit.ActionNodeLabelsUpdate,
		TargetNode: nodeID,
		Detail:     nodeLabelsAuditDetail(labels),
		CreatedAt:  now,
	})
	writeJSON(w, http.StatusOK, protocol.NodeLabelsResponse{
		NodeID: nodeID,
		Labels: labels,
	})
}

func nodeLabelsAuditDetail(labels map[string]string) string {
	if len(labels) == 0 {
		return "labels cleared"
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return "labels=" + strings.Join(keys, ",")
}

func (h *handler) setNodeMaintenance(w http.ResponseWriter, r *http.Request, nodeID string) {
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.NodeMaintenanceRequest
	if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid node maintenance JSON")
		return
	}
	if err := h.store.SetNodeMaintenance(r.Context(), nodeID, req.Maintenance); err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			writeAPIError(w, http.StatusNotFound, "node not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "set node maintenance")
		return
	}
	state := "off"
	if req.Maintenance {
		state = "on"
	}
	now := time.Now().UTC()
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:      audit.ActorOperator,
		Action:     audit.ActionNodeMaintenanceUpdate,
		TargetNode: nodeID,
		Detail:     "maintenance=" + state,
		CreatedAt:  now,
	})
	writeJSON(w, http.StatusOK, protocol.NodeMaintenanceResponse{
		NodeID:      nodeID,
		Maintenance: req.Maintenance,
	})
}

func (h *handler) deleteNode(w http.ResponseWriter, r *http.Request, nodeID string) {
	if !h.authorizeOperator(w, r) {
		return
	}

	if err := h.store.DeleteNode(r.Context(), nodeID); err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			writeAPIError(w, http.StatusNotFound, "node not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "delete node")
		return
	}

	h.audit(r.Context(), protocol.AuditEvent{
		Actor:      audit.ActorOperator,
		Action:     audit.ActionNodeDelete,
		TargetNode: nodeID,
		Detail:     "node removed from inventory",
		CreatedAt:  time.Now().UTC(),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) listNodeJobs(w http.ResponseWriter, r *http.Request, nodeID string) {
	if !h.authorizeOperatorRead(w, r) {
		return
	}
	filter, err := parseJobFilter(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	jobs, err := h.store.ListNodeJobsFiltered(r.Context(), nodeID, filter)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "list node jobs")
		return
	}
	h.observeTimedOutJobs(r.Context(), jobs)
	jobs = redactJobs(jobs)
	if h.shouldHideJobResults(r) {
		jobs = summarizeJobs(jobs)
	}

	writeJSON(w, http.StatusOK, jobs)
}

func parseJobFilter(r *http.Request) (store.JobFilter, error) {
	filter := store.JobFilter{Limit: store.DefaultJobListLimit}
	query := r.URL.Query()
	if limitValue := strings.TrimSpace(query.Get("limit")); limitValue != "" {
		limit, err := strconv.Atoi(limitValue)
		if err != nil || limit <= 0 {
			return store.JobFilter{}, fmt.Errorf("limit must be a positive integer")
		}
		if limit > store.MaxJobListLimit {
			limit = store.MaxJobListLimit
		}
		filter.Limit = limit
	}
	if statusValue := strings.TrimSpace(query.Get("status")); statusValue != "" {
		status := protocol.JobStatus(statusValue)
		if !validJobStatus(status) {
			return store.JobFilter{}, fmt.Errorf("unsupported job status %q", statusValue)
		}
		filter.Status = status
	}
	return filter, nil
}

func validJobStatus(status protocol.JobStatus) bool {
	switch status {
	case protocol.JobStatusPending, protocol.JobStatusClaimed, protocol.JobStatusCompleted, protocol.JobStatusFailed:
		return true
	default:
		return false
	}
}

func (h *handler) shouldHideJobResults(r *http.Request) bool {
	return h.operatorAuth.Configured() && !h.operatorAuth.AuthorizeHeader(r.Header.Get("Authorization"))
}

func summarizeJobs(jobs []protocol.Job) []protocol.Job {
	summaries := make([]protocol.Job, len(jobs))
	copy(summaries, jobs)
	for i := range summaries {
		summaries[i].ResultJSON = ""
	}
	return summaries
}

func redactJobs(jobs []protocol.Job) []protocol.Job {
	redacted := make([]protocol.Job, len(jobs))
	copy(redacted, jobs)
	for i := range redacted {
		redacted[i].PayloadJSON = spconfig.RedactString(redacted[i].PayloadJSON)
		redacted[i].ResultJSON = spconfig.RedactString(redacted[i].ResultJSON)
		redacted[i].Error = spconfig.RedactString(redacted[i].Error)
	}
	return redacted
}

func (h *handler) createNodeJob(w http.ResponseWriter, r *http.Request, nodeID string) {
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.CreateJobRequest
	if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid job JSON")
		return
	}

	if req.Type == "" {
		writeAPIError(w, http.StatusBadRequest, "type is required")
		return
	}
	if req.Type != protocol.JobTypeDeepProbe {
		writeAPIError(w, http.StatusBadRequest, "unsupported job type")
		return
	}

	exists, err := h.store.NodeExists(r.Context(), nodeID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "lookup node")
		return
	}
	if !exists {
		writeAPIError(w, http.StatusNotFound, "node not found")
		return
	}

	job, err := h.store.CreateJob(r.Context(), req, nodeID, time.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrActiveJobExists) {
			writeAPIError(w, http.StatusConflict, "active job already exists")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "create job")
		return
	}
	h.metrics.IncJobCreated(string(req.Type))
	h.logger.Info("job created", "job_id", job.ID, "node_id", nodeID, "type", job.Type, "status", job.Status)
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:      audit.ActorOperator,
		Action:     audit.ActionJobCreate,
		TargetNode: nodeID,
		Detail:     string(req.Type),
		CreatedAt:  job.CreatedAt,
	})
	publishJobEvent(h.events, job)

	writeJSON(w, http.StatusCreated, job)
}

func (h *handler) bulkJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.BulkJobRequest
	if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid bulk job JSON")
		return
	}
	if req.Type == "" {
		writeAPIError(w, http.StatusBadRequest, "type is required")
		return
	}
	if req.Type != protocol.JobTypeDeepProbe {
		writeAPIError(w, http.StatusBadRequest, "unsupported job type")
		return
	}
	nodeIDs := uniqueTrimmedStrings(req.NodeIDs)
	selector, err := store.ValidateNodeLabels(req.Selector)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid selector: "+err.Error())
		return
	}
	if len(nodeIDs) > 0 && len(selector) > 0 {
		writeAPIError(w, http.StatusBadRequest, "selector and nodeIds are mutually exclusive")
		return
	}
	if len(nodeIDs) == 0 && len(selector) == 0 {
		writeAPIError(w, http.StatusBadRequest, "selector or nodeIds is required")
		return
	}

	targets, err := h.resolveTargetNodes(r.Context(), selector, nodeIDs, req.IncludeMaintenance)
	if err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			writeAPIError(w, http.StatusNotFound, "node not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "resolve bulk job nodes")
		return
	}
	if len(targets) == 0 {
		writeAPIError(w, http.StatusBadRequest, "bulk job target set is empty")
		return
	}

	now := time.Now().UTC()
	results := make([]protocol.BulkJobResult, 0, len(targets))
	created := 0
	for _, nodeID := range targets {
		job, err := h.store.CreateJob(r.Context(), protocol.CreateJobRequest{Type: req.Type}, nodeID, now)
		if err != nil {
			// A conflicting active job is a per-node skip, not a request failure.
			if errors.Is(err, store.ErrActiveJobExists) {
				results = append(results, protocol.BulkJobResult{NodeID: nodeID, Error: "active job already exists"})
				continue
			}
			results = append(results, protocol.BulkJobResult{NodeID: nodeID, Error: "create job failed"})
			h.logger.Warn("bulk job creation failed for node", "node_id", nodeID, "error", err)
			continue
		}
		created++
		h.metrics.IncJobCreated(string(req.Type))
		results = append(results, protocol.BulkJobResult{NodeID: nodeID, JobID: job.ID})
		publishJobEvent(h.events, job)
	}

	h.logger.Info("bulk jobs created", "type", req.Type, "matched", len(targets), "created", created)
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    audit.ActionJobBulkCreate,
		Detail:    fmt.Sprintf("type=%s matched=%d created=%d", req.Type, len(targets), created),
		CreatedAt: now,
	})
	writeJSON(w, http.StatusCreated, protocol.BulkJobResponse{Jobs: results, Created: created})
}

func (h *handler) bulkNodeLabels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", http.MethodPut)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.BulkNodeLabelsRequest
	if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid bulk labels JSON")
		return
	}
	labels, err := store.ValidateNodeLabels(req.Labels)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid labels: "+err.Error())
		return
	}
	if len(labels) == 0 {
		writeAPIError(w, http.StatusBadRequest, "labels are required")
		return
	}
	nodeIDs := uniqueTrimmedStrings(req.NodeIDs)
	selector, err := store.ValidateNodeLabels(req.Selector)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid selector: "+err.Error())
		return
	}
	if len(nodeIDs) > 0 && len(selector) > 0 {
		writeAPIError(w, http.StatusBadRequest, "selector and nodeIds are mutually exclusive")
		return
	}
	if len(nodeIDs) == 0 && len(selector) == 0 {
		writeAPIError(w, http.StatusBadRequest, "selector or nodeIds is required")
		return
	}

	targets, err := h.resolveTargetNodes(r.Context(), selector, nodeIDs, req.IncludeMaintenance)
	if err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			writeAPIError(w, http.StatusNotFound, "node not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "resolve label target nodes")
		return
	}
	if len(targets) == 0 {
		writeAPIError(w, http.StatusBadRequest, "label target set is empty")
		return
	}

	for _, nodeID := range targets {
		existing, err := h.store.GetNodeLabels(r.Context(), nodeID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "read node labels")
			return
		}
		merged := make(map[string]string, len(existing)+len(labels))
		for key, value := range existing {
			merged[key] = value
		}
		for key, value := range labels {
			merged[key] = value
		}
		if err := h.store.SetNodeLabels(r.Context(), nodeID, merged); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "set node labels")
			return
		}
	}

	now := time.Now().UTC()
	h.logger.Info("bulk node labels applied", "matched", len(targets), "keys", len(labels))
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    audit.ActionNodeLabelsBulkUpdate,
		Detail:    fmt.Sprintf("nodes=%d keys=%s", len(targets), labelKeysSummary(labels)),
		CreatedAt: now,
	})
	writeJSON(w, http.StatusOK, protocol.BulkNodeLabelsResponse{NodeIDs: targets, Updated: len(targets)})
}

// labelKeysSummary renders the sorted set of label keys for audit detail.
func labelKeysSummary(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func (h *handler) createRestartJob(w http.ResponseWriter, r *http.Request, nodeID string) {
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.RestartRequest
	if err := decodeOptionalJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid restart JSON")
		return
	}

	runtimeType := strings.TrimSpace(req.RuntimeType)
	if runtimeType != "" && runtimeType != "hermes" && runtimeType != "openclaw" {
		writeAPIError(w, http.StatusBadRequest, "unsupported runtime type")
		return
	}
	runtimeName := strings.TrimSpace(req.RuntimeName)
	profile := strings.TrimSpace(req.Profile)
	reason := strings.TrimSpace(req.Reason)

	exists, err := h.store.NodeExists(r.Context(), nodeID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "lookup node")
		return
	}
	if !exists {
		writeAPIError(w, http.StatusNotFound, "node not found")
		return
	}

	payload, err := json.Marshal(protocol.RestartJobPayload{
		RuntimeType: runtimeType,
		RuntimeName: runtimeName,
		Profile:     profile,
		Reason:      reason,
		DryRun:      !req.Live,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "marshal restart payload")
		return
	}

	now := time.Now().UTC()
	job, err := h.store.CreateJob(r.Context(), protocol.CreateJobRequest{
		Type:        protocol.JobTypeRestart,
		PayloadJSON: string(payload),
	}, nodeID, now)
	if err != nil {
		if errors.Is(err, store.ErrActiveJobExists) {
			writeAPIError(w, http.StatusConflict, "active restart job already exists")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "create restart job")
		return
	}

	mode := "dry-run"
	if req.Live {
		mode = "live"
	}
	target := runtimeTargetSummary(runtimeType, runtimeName, profile)
	h.metrics.IncJobCreated(string(protocol.JobTypeRestart))
	h.logger.Info("job created", "job_id", job.ID, "node_id", nodeID, "type", job.Type, "status", job.Status, "mode", mode)
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:      audit.ActorOperator,
		Action:     audit.ActionRestart,
		TargetNode: nodeID,
		Detail:     fmt.Sprintf("job=%s mode=%s target=%s", job.ID, mode, target),
		CreatedAt:  job.CreatedAt,
	})
	publishJobEvent(h.events, job)

	writeJSON(w, http.StatusCreated, job)
}

func runtimeTargetSummary(runtimeType, runtimeName, profile string) string {
	parts := []string{}
	if runtimeType != "" {
		parts = append(parts, "type="+runtimeType)
	}
	if runtimeName != "" {
		parts = append(parts, "name="+runtimeName)
	}
	if profile != "" {
		parts = append(parts, "profile="+profile)
	}
	if len(parts) == 0 {
		return "default"
	}
	return strings.Join(parts, " ")
}

func (h *handler) createRollbackJob(w http.ResponseWriter, r *http.Request, nodeID string) {
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.RollbackRequest
	if err := decodeOptionalJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid rollback JSON")
		return
	}
	backupRef := strings.TrimSpace(req.BackupRef)
	if backupRef == "" {
		writeAPIError(w, http.StatusBadRequest, "backupRef is required")
		return
	}
	runtimeType := strings.TrimSpace(req.RuntimeType)
	if runtimeType != "" && runtimeType != "hermes" && runtimeType != "openclaw" {
		writeAPIError(w, http.StatusBadRequest, "unsupported runtime type")
		return
	}
	runtimeName := strings.TrimSpace(req.RuntimeName)
	profile := strings.TrimSpace(req.Profile)

	exists, err := h.store.NodeExists(r.Context(), nodeID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "lookup node")
		return
	}
	if !exists {
		writeAPIError(w, http.StatusNotFound, "node not found")
		return
	}

	backup, ok, err := h.findRollbackBackup(r.Context(), nodeID, backupRef)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "lookup rollback backup")
		return
	}
	if !ok {
		writeAPIError(w, http.StatusNotFound, "rollback backup not found")
		return
	}
	if runtimeType == "" {
		runtimeType = backup.RuntimeType
	}
	if profile == "" {
		profile = backup.Profile
	}
	if backup.RuntimeType != "" && runtimeType != "" && backup.RuntimeType != runtimeType {
		writeAPIError(w, http.StatusBadRequest, "rollback backup runtime type mismatch")
		return
	}
	if backup.Profile != "" && profile != "" && backup.Profile != profile {
		writeAPIError(w, http.StatusBadRequest, "rollback backup profile mismatch")
		return
	}

	payload, err := json.Marshal(protocol.RollbackJobPayload{
		RuntimeType: runtimeType,
		RuntimeName: runtimeName,
		Profile:     profile,
		BackupRef:   backup.Ref,
		ConfigPath:  backup.ConfigPath,
		BackupPath:  backup.BackupPath,
		DryRun:      !req.Live,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "marshal rollback payload")
		return
	}

	now := time.Now().UTC()
	job, err := h.store.CreateJob(r.Context(), protocol.CreateJobRequest{
		Type:        protocol.JobTypeRollback,
		PayloadJSON: string(payload),
	}, nodeID, now)
	if err != nil {
		if errors.Is(err, store.ErrActiveJobExists) {
			writeAPIError(w, http.StatusConflict, "active rollback job already exists")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "create rollback job")
		return
	}

	mode := "dry-run"
	if req.Live {
		mode = "live"
	}
	h.metrics.IncJobCreated(string(protocol.JobTypeRollback))
	h.logger.Info("job created", "job_id", job.ID, "node_id", nodeID, "type", job.Type, "status", job.Status, "mode", mode)
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:      audit.ActorOperator,
		Action:     audit.ActionRollback,
		TargetNode: nodeID,
		Detail:     fmt.Sprintf("job=%s mode=%s backupRef=%s target=%s", job.ID, mode, backup.Ref, runtimeTargetSummary(runtimeType, runtimeName, profile)),
		CreatedAt:  job.CreatedAt,
	})
	publishJobEvent(h.events, job)

	writeJSON(w, http.StatusCreated, job)
}

func (h *handler) findRollbackBackup(ctx context.Context, nodeID string, backupRef string) (protocol.RollbackBackup, bool, error) {
	jobs, err := h.store.ListNodeJobs(ctx, nodeID)
	if err != nil {
		return protocol.RollbackBackup{}, false, err
	}
	for _, backup := range store.ListRollbackBackups(jobs) {
		if backup.Ref == backupRef {
			return backup, true, nil
		}
	}
	return protocol.RollbackBackup{}, false, nil
}

// createConfigApplyJob builds a signed config plan from the desired config and
// enqueues a config_apply job for the node. It defaults to a dry-run plan.
func (h *handler) createConfigApplyJob(w http.ResponseWriter, r *http.Request, nodeID string) {
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.ConfigApplyRequest
	if err := decodeOptionalJSONRequest(w, r, largeJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid config apply JSON")
		return
	}

	runtimeType := strings.TrimSpace(req.RuntimeType)
	if runtimeType == "" {
		runtimeType = "hermes"
	}
	if runtimeType != "hermes" {
		writeAPIError(w, http.StatusBadRequest, "unsupported runtime type")
		return
	}
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	profile := strings.TrimSpace(req.Profile)

	if len(h.signingKey.PrivateKey) == 0 {
		writeAPIError(w, http.StatusServiceUnavailable, "server signing key is not configured")
		return
	}

	exists, err := h.store.NodeExists(r.Context(), nodeID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "lookup node")
		return
	}
	if !exists {
		writeAPIError(w, http.StatusNotFound, "node not found")
		return
	}

	desired, err := h.store.GetDesiredConfig(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "get desired config")
		return
	}
	target := spconfig.EffectiveConfigTarget{
		NodeID:      nodeID,
		RuntimeType: runtimeType,
		Profile:     profile,
	}
	effective := spconfig.EffectiveProviderModelConfig(desired, target)
	if strings.TrimSpace(effective.Provider) == "" || strings.TrimSpace(effective.Model) == "" {
		writeAPIError(w, http.StatusBadRequest, "desired provider and model must be set before applying config")
		return
	}
	if err := spconfig.ValidateProviderModelSelection(effective); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid desired provider/model: "+err.Error())
		return
	}

	actual, err := h.latestActualSnapshot(r.Context(), nodeID, runtimeType, profile)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "get actual config")
		return
	}
	if actual == nil || strings.TrimSpace(actual.ConfigPath) == "" {
		writeAPIError(w, http.StatusBadRequest, "no known config path for node; run a deep probe first")
		return
	}

	now := time.Now().UTC()
	planID, err := newPlanID()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "generate plan id")
		return
	}
	mode := protocol.ConfigPlanModeDryRun
	if !dryRun {
		mode = protocol.ConfigPlanModeLive
	}
	plan := protocol.ConfigPlan{
		ID:           planID,
		Schema:       protocol.ConfigPlanSchema,
		Version:      protocol.ConfigPlanVersion,
		CreatedAt:    now,
		TargetNodeID: nodeID,
		Mode:         mode,
		Body: protocol.ConfigPlanBody{
			RuntimeType: runtimeType,
			// Profile carries the read-only config path the sidecar reads/backs up.
			Profile:   actual.ConfigPath,
			Desired:   effective,
			Providers: spconfig.EffectiveProviderCatalog(desired, target),
			DryRun:    dryRun,
		},
	}
	signed, err := protocol.SignConfigPlan(plan, h.signingKey.PrivateKey)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "sign config plan")
		return
	}
	payload, err := json.Marshal(signed)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "marshal signed plan")
		return
	}

	job, err := h.store.CreateJob(r.Context(), protocol.CreateJobRequest{
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: string(payload),
	}, nodeID, now)
	if err != nil {
		if errors.Is(err, store.ErrActiveJobExists) {
			writeAPIError(w, http.StatusConflict, "active config_apply job already exists")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "create job")
		return
	}
	h.metrics.IncJobCreated(string(protocol.JobTypeConfigApply))
	h.logger.Info("job created", "job_id", job.ID, "node_id", nodeID, "type", job.Type, "status", job.Status, "mode", mode, "plan_id", planID)
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:      audit.ActorOperator,
		Action:     audit.ActionConfigApply,
		TargetNode: nodeID,
		Detail:     fmt.Sprintf("%s %s plan=%s", runtimeType, mode, planID),
		CreatedAt:  job.CreatedAt,
	})
	publishJobEvent(h.events, job)

	writeJSON(w, http.StatusCreated, job)
}

func newPlanID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "plan_" + hex.EncodeToString(buf), nil
}

// claimNextJob handles GET /api/sidecar/jobs/next?nodeId=...
func (h *handler) claimNextJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}

	nodeID := strings.TrimSpace(r.URL.Query().Get("nodeId"))
	if nodeID == "" {
		writeAPIError(w, http.StatusBadRequest, "nodeId query parameter is required")
		return
	}

	credential, ok := auth.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized))
		return
	}

	ok, err := h.store.VerifyNodeCredential(r.Context(), nodeID, credential)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "verify node credential")
		return
	}
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized))
		return
	}

	job, err := h.store.ClaimNextJob(r.Context(), nodeID, time.Now().UTC())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "claim next job")
		return
	}

	if job == nil {
		jobs, listErr := h.store.ListNodeJobs(r.Context(), nodeID)
		if listErr == nil {
			h.observeTimedOutJobs(r.Context(), jobs)
		}
		// No pending jobs
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	h.metrics.IncSidecarJobClaim(string(job.Type))
	h.logger.Info("job claimed", "job_id", job.ID, "node_id", nodeID, "type", job.Type, "status", job.Status)
	publishJobEvent(h.events, *job)
	writeJSON(w, http.StatusOK, job)
}

// submitJobResult handles POST /api/sidecar/jobs/{jobId}/result
func (h *handler) submitJobResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}

	// Parse path: /api/sidecar/jobs/{jobId}/result
	path := strings.TrimPrefix(r.URL.Path, "/api/sidecar/jobs/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "result" {
		writeAPIError(w, http.StatusNotFound, http.StatusText(http.StatusNotFound))
		return
	}
	jobID := strings.TrimSpace(parts[0])
	if jobID == "" {
		writeAPIError(w, http.StatusNotFound, http.StatusText(http.StatusNotFound))
		return
	}

	credential, ok := auth.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized))
		return
	}

	// Load job to get nodeId for credential verification
	job, err := h.store.GetJob(r.Context(), jobID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "get job")
		return
	}
	if job == nil {
		writeAPIError(w, http.StatusNotFound, "job not found")
		return
	}

	ok, err = h.store.VerifyNodeCredential(r.Context(), job.NodeID, credential)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "verify node credential")
		return
	}
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized))
		return
	}

	var req protocol.JobResultRequest
	if err := decodeJSONRequest(w, r, largeJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid result JSON")
		return
	}
	req.ResultJSON = spconfig.RedactString(req.ResultJSON)
	req.Error = spconfig.RedactString(req.Error)

	now := time.Now().UTC()
	if store.IsJobClaimTimeout(*job) {
		h.observeTimedOutJobs(r.Context(), []protocol.Job{*job})
	}
	lateResult := false
	if req.Status == protocol.JobStatusCompleted {
		err = h.store.CompleteJob(r.Context(), jobID, req, now)
	} else if req.Status == protocol.JobStatusFailed {
		err = h.store.FailJob(r.Context(), jobID, req, now)
	} else {
		writeAPIError(w, http.StatusBadRequest, "status must be completed or failed")
		return
	}

	if err != nil {
		if errors.Is(err, store.ErrLateJobResultRecorded) {
			lateResult = true
		} else {
			writeAPIError(w, http.StatusInternalServerError, "submit job result")
			return
		}
	}
	if lateResult {
		h.metrics.IncLateJobResult(string(job.Type), string(req.Status))
		h.logger.Warn("late job result recorded", "job_id", job.ID, "node_id", job.NodeID, "type", job.Type, "status", req.Status)
		h.audit(r.Context(), protocol.AuditEvent{
			Actor:      audit.ActorSidecar,
			Action:     audit.ActionJobFail,
			TargetNode: job.NodeID,
			Detail:     string(job.Type) + " late_result_after_timeout",
			CreatedAt:  now,
		})
		publishJobEvent(h.events, protocol.Job{ID: job.ID, NodeID: job.NodeID, Type: job.Type, Status: protocol.JobStatusFailed})
		writeJSON(w, http.StatusOK, map[string]string{"status": "accepted_late"})
		return
	}

	action := audit.ActionJobComplete
	if req.Status == protocol.JobStatusFailed {
		action = audit.ActionJobFail
		h.metrics.IncJobFailed(string(job.Type))
	} else {
		h.metrics.IncJobCompleted(string(job.Type))
	}
	if job.Type == protocol.JobTypeConfigApply && configApplyRolledBack(req.ResultJSON) {
		h.metrics.IncConfigApplyRolledBack()
	}
	h.logger.Info("job result recorded", "job_id", job.ID, "node_id", job.NodeID, "type", job.Type, "status", req.Status)
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:      audit.ActorSidecar,
		Action:     action,
		TargetNode: job.NodeID,
		Detail:     string(job.Type),
		CreatedAt:  now,
	})
	publishJobEvent(h.events, protocol.Job{ID: job.ID, NodeID: job.NodeID, Type: job.Type, Status: req.Status})

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

// configApplyRolledBack reports whether a config apply result recorded a
// completed rollback step. It tolerates missing or malformed result payloads.
func configApplyRolledBack(resultJSON string) bool {
	if strings.TrimSpace(resultJSON) == "" {
		return false
	}
	var result protocol.ConfigApplyResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return false
	}
	for _, step := range result.Steps {
		if step.Name == "rolled_back" && step.Status == "completed" {
			return true
		}
	}
	return false
}

func (h *handler) observeTimedOutJobs(ctx context.Context, jobs []protocol.Job) {
	for _, job := range jobs {
		if !store.IsJobClaimTimeout(job) {
			continue
		}
		if !h.markTimedOutJobObserved(job.ID) {
			continue
		}
		h.metrics.IncJobFailed(string(job.Type))
		h.logger.Warn("job timed out", "job_id", job.ID, "node_id", job.NodeID, "type", job.Type, "status", job.Status)
		createdAt := job.FinishedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		h.audit(ctx, protocol.AuditEvent{
			Actor:      audit.ActorSidecar,
			Action:     audit.ActionJobFail,
			TargetNode: job.NodeID,
			Detail:     string(job.Type) + " timeout",
			CreatedAt:  createdAt,
		})
		publishJobEvent(h.events, job)
	}
}

func (h *handler) markTimedOutJobObserved(jobID string) bool {
	h.timedOutMu.Lock()
	defer h.timedOutMu.Unlock()
	if _, ok := h.timedOutJobs[jobID]; ok {
		return false
	}
	h.timedOutJobs[jobID] = struct{}{}
	return true
}

func (h *handler) auditEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if !h.authorizeOperatorRead(w, r) {
		return
	}

	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid limit query parameter")
			return
		}
		limit = parsed
	}

	events, err := h.store.ListAuditEventsFiltered(r.Context(), store.AuditFilter{
		NodeID: strings.TrimSpace(r.URL.Query().Get("nodeId")),
		Action: strings.TrimSpace(r.URL.Query().Get("action")),
		Limit:  limit,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "list audit events")
		return
	}
	events = redactAuditEvents(events)
	writeJSON(w, http.StatusOK, protocol.ListAuditEventsResponse{Events: events})
}

func (h *handler) exportAuditEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if !h.authorizeOperator(w, r) {
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "ndjson"
	}
	if format != "ndjson" && format != "csv" {
		writeAPIError(w, http.StatusBadRequest, "format must be ndjson or csv")
		return
	}

	limit := maxAuditExportLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid limit query parameter")
			return
		}
		limit = parsed
	}

	events, err := h.store.ListAuditEventsFiltered(r.Context(), store.AuditFilter{
		NodeID: strings.TrimSpace(r.URL.Query().Get("nodeId")),
		Action: strings.TrimSpace(r.URL.Query().Get("action")),
		Limit:  limit,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "list audit events")
		return
	}
	events = redactAuditEvents(events)

	controller := http.NewResponseController(w)
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="sideplane-audit.csv"`)
		w.WriteHeader(http.StatusOK)
		writer := csv.NewWriter(w)
		_ = writer.Write([]string{"id", "actor", "action", "targetNode", "detail", "createdAt"})
		for _, event := range events {
			_ = writer.Write([]string{
				event.ID,
				event.Actor,
				event.Action,
				event.TargetNode,
				event.Detail,
				formatExportTime(event.CreatedAt),
			})
			writer.Flush()
			_ = controller.Flush()
		}
		writer.Flush()
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="sideplane-audit.ndjson"`)
	w.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(w)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return
		}
		_ = controller.Flush()
	}
}

func formatExportTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (h *handler) publicSigningKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if len(h.signingKey.PublicKey) == 0 {
		writeAPIError(w, http.StatusServiceUnavailable, "signing key unavailable")
		return
	}
	writeJSON(w, http.StatusOK, protocol.PublicSigningKeyResponse{
		Algorithm: "ed25519",
		PublicKey: spcrypto.PublicKeyString(h.signingKey.PublicKey),
	})
}

func (h *handler) desiredConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !h.authorizeOperatorRead(w, r) {
			return
		}
		desired, err := h.store.GetDesiredConfig(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "get desired config")
			return
		}
		writeJSON(w, http.StatusOK, desired)
	case http.MethodPut:
		if !h.authorizeOperator(w, r) {
			return
		}
		var desired protocol.DesiredConfig
		if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &desired); err != nil {
			writeJSONDecodeError(w, err, "invalid desired config JSON")
			return
		}
		if err := spconfig.ValidateDesiredConfigValues(desired); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid desired config: "+err.Error())
			return
		}
		now := time.Now().UTC()
		if err := h.store.SetDesiredConfig(r.Context(), desired, now); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "set desired config")
			return
		}
		h.audit(r.Context(), protocol.AuditEvent{
			Actor:     audit.ActorOperator,
			Action:    audit.ActionDesiredConfigUpdate,
			Detail:    "desiredHash=" + hashDesiredConfig(desired),
			CreatedAt: now,
		})
		writeJSON(w, http.StatusOK, desired)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPut)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
	}
}

func (h *handler) configProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !h.authorizeOperatorRead(w, r) {
			return
		}
		scope := providerScopeFromQuery(r)
		desired, err := h.store.GetDesiredConfig(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "get desired config")
			return
		}
		h.writeProviderCatalogResponse(w, r, desired, scope)
	case http.MethodPut:
		if !h.authorizeOperator(w, r) {
			return
		}
		var req protocol.UpsertProviderRequest
		if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
			writeJSONDecodeError(w, err, "invalid provider catalog JSON")
			return
		}
		if err := validateProviderDefinitionForUpsert(req.Provider); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid provider: "+err.Error())
			return
		}
		apiKey := strings.TrimSpace(req.APIKey)
		envName := strings.TrimSpace(req.Provider.APIKeyEnv)
		if apiKey != "" && envName == "" {
			writeAPIError(w, http.StatusBadRequest, "apiKeyEnv is required when apiKey is provided")
			return
		}
		if apiKey != "" && len(h.secretKey) == 0 {
			writeAPIError(w, http.StatusConflict, "server secret key not configured (set --secret-key)")
			return
		}
		scope := providerScopeFromProtocol(req.Scope)
		desired, err := h.store.GetDesiredConfig(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "get desired config")
			return
		}
		updated := spconfig.UpsertProviderDefinition(desired, scope, req.Provider)
		if err := spconfig.ValidateDesiredConfigValues(updated); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid desired config: "+err.Error())
			return
		}
		now := time.Now().UTC()
		if apiKey != "" {
			ciphertext, err := spcrypto.Encrypt(h.secretKey, []byte(apiKey))
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, "encrypt provider secret")
				return
			}
			if err := h.store.SetProviderSecret(r.Context(), envName, ciphertext, now); err != nil {
				writeAPIError(w, http.StatusInternalServerError, "store provider secret")
				return
			}
		}
		if err := h.store.SetDesiredConfig(r.Context(), updated, now); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "set desired config")
			return
		}
		managed, _ := h.providerSecretManaged(r.Context(), envName)
		h.audit(r.Context(), protocol.AuditEvent{
			Actor:     audit.ActorOperator,
			Action:    audit.ActionDesiredConfigUpdate,
			Detail:    fmt.Sprintf("provider=%s env=%s managed=%t scope=%s desiredHash=%s", strings.TrimSpace(req.Provider.Name), envName, managed, formatProviderScope(scope), hashDesiredConfig(updated)),
			CreatedAt: now,
		})
		h.writeProviderCatalogResponse(w, r, updated, scope)
	case http.MethodDelete:
		if !h.authorizeOperator(w, r) {
			return
		}
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if name == "" {
			writeAPIError(w, http.StatusBadRequest, "name query parameter is required")
			return
		}
		scope := providerScopeFromQuery(r)
		desired, err := h.store.GetDesiredConfig(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "get desired config")
			return
		}
		removedProvider, _ := providerDefinitionInScope(desired, scope, name)
		updated, removed := spconfig.RemoveProviderDefinition(desired, scope, name)
		if !removed {
			writeAPIError(w, http.StatusNotFound, "provider not found")
			return
		}
		if err := spconfig.ValidateDesiredConfigValues(updated); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid desired config: "+err.Error())
			return
		}
		now := time.Now().UTC()
		if err := h.store.SetDesiredConfig(r.Context(), updated, now); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "set desired config")
			return
		}
		envName := strings.TrimSpace(removedProvider.APIKeyEnv)
		managed, _ := h.providerSecretManaged(r.Context(), envName)
		h.audit(r.Context(), protocol.AuditEvent{
			Actor:     audit.ActorOperator,
			Action:    audit.ActionDesiredConfigUpdate,
			Detail:    fmt.Sprintf("provider=%s env=%s managed=%t scope=%s desiredHash=%s secretRetained=true", name, envName, managed, formatProviderScope(scope), hashDesiredConfig(updated)),
			CreatedAt: now,
		})
		h.writeProviderCatalogResponse(w, r, updated, scope)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
	}
}

func (h *handler) desiredConfigHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if !h.authorizeOperator(w, r) {
		return
	}
	filter, err := parseDesiredConfigHistoryFilter(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	history, err := h.store.ListDesiredConfigHistory(r.Context(), filter)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "list desired config history")
		return
	}
	writeJSON(w, http.StatusOK, protocol.ListDesiredConfigHistoryResponse{
		History: history.History,
		Total:   history.Total,
		Limit:   history.Limit,
		Offset:  history.Offset,
	})
}

func (h *handler) revertDesiredConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.RevertDesiredConfigRequest
	if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid desired config revert JSON")
		return
	}
	historyID := strings.TrimSpace(req.HistoryID)
	if historyID == "" {
		writeAPIError(w, http.StatusBadRequest, "historyId is required")
		return
	}
	candidate, found, err := h.findDesiredConfigHistoryEntry(r.Context(), historyID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "get desired config history")
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "desired config history not found")
		return
	}
	if err := spconfig.ValidateDesiredConfigValues(candidate.Config); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid desired config: "+err.Error())
		return
	}
	entry, err := h.store.RevertDesiredConfig(r.Context(), historyID)
	if errors.Is(err, store.ErrDesiredConfigHistoryNotFound) {
		writeAPIError(w, http.StatusNotFound, "desired config history not found")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "revert desired config")
		return
	}
	now := time.Now().UTC()
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    audit.ActionDesiredConfigRevert,
		Detail:    fmt.Sprintf("historyId=%s desiredHash=%s", historyID, hashDesiredConfig(entry.Config)),
		CreatedAt: now,
	})
	writeJSON(w, http.StatusOK, protocol.RevertDesiredConfigResponse{
		Desired: entry.Config,
		History: entry,
	})
}

func (h *handler) effectiveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if !h.authorizeOperatorRead(w, r) {
		return
	}
	nodeID := strings.TrimSpace(r.URL.Query().Get("nodeId"))
	if nodeID == "" {
		writeAPIError(w, http.StatusBadRequest, "nodeId query parameter is required")
		return
	}
	runtimeType := strings.TrimSpace(r.URL.Query().Get("runtimeType"))
	profile := strings.TrimSpace(r.URL.Query().Get("profile"))

	desired, err := h.store.GetDesiredConfig(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "get desired config")
		return
	}
	h.writeEffectiveConfig(w, r, nodeID, runtimeType, profile, desired)
}

func (h *handler) previewEffectiveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
		return
	}
	if !h.authorizeOperator(w, r) {
		return
	}
	var req protocol.EffectiveConfigPreviewRequest
	if err := decodeJSONRequest(w, r, defaultJSONBodyLimit, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid config preview JSON")
		return
	}
	if err := spconfig.ValidateProviderModelSelection(req.Desired); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid desired provider/model: "+err.Error())
		return
	}

	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		writeAPIError(w, http.StatusBadRequest, "nodeId is required")
		return
	}
	runtimeType := strings.TrimSpace(req.RuntimeType)
	profile := strings.TrimSpace(req.Profile)

	desired, err := h.store.GetDesiredConfig(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "get desired config")
		return
	}
	target := spconfig.EffectiveConfigTarget{
		NodeID:      nodeID,
		RuntimeType: runtimeType,
		Profile:     profile,
	}
	previewDesired := spconfig.DesiredConfigWithTargetOverride(desired, target, req.Desired)
	h.writeEffectiveConfig(w, r, nodeID, runtimeType, profile, previewDesired)
}

func (h *handler) writeProviderCatalogResponse(w http.ResponseWriter, r *http.Request, desired protocol.DesiredConfig, scope spconfig.ProviderScope) {
	response, err := h.providerCatalogResponse(r.Context(), desired, scope)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "get actual provider catalog")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) providerCatalogResponse(ctx context.Context, desired protocol.DesiredConfig, scope spconfig.ProviderScope) (protocol.ProviderCatalogResponse, error) {
	scope = normalizeHTTPProviderScope(scope)
	global, err := h.providerDefinitionsWithSecretState(ctx, desired.GlobalProviders)
	if err != nil {
		return protocol.ProviderCatalogResponse{}, err
	}
	nodeProviders, err := h.providerDefinitionMapWithSecretState(ctx, desired.NodeProviders)
	if err != nil {
		return protocol.ProviderCatalogResponse{}, err
	}
	runtimeProfileProviders, err := h.providerDefinitionMapWithSecretState(ctx, desired.RuntimeProfileProviders)
	if err != nil {
		return protocol.ProviderCatalogResponse{}, err
	}
	nodeRuntimeProfileProviders, err := h.providerDefinitionMapWithSecretState(ctx, desired.NodeRuntimeProfileProviders)
	if err != nil {
		return protocol.ProviderCatalogResponse{}, err
	}
	effective, err := h.providerDefinitionsWithSecretState(ctx, spconfig.EffectiveProviderCatalog(desired, spconfig.EffectiveConfigTarget{
		NodeID:      scope.NodeID,
		RuntimeType: scope.RuntimeType,
		Profile:     scope.Profile,
	}))
	if err != nil {
		return protocol.ProviderCatalogResponse{}, err
	}
	response := protocol.ProviderCatalogResponse{
		Global:             global,
		NodeProviders:      nodeProviders,
		RuntimeProfile:     runtimeProfileProviders,
		NodeRuntimeProfile: nodeRuntimeProfileProviders,
		Effective:          effective,
	}
	if scope.NodeID == "" {
		return response, nil
	}
	snapshots, err := h.latestActualSnapshots(ctx, scope.NodeID)
	if err != nil {
		return protocol.ProviderCatalogResponse{}, err
	}
	for _, snapshot := range snapshots {
		if !runtimeConfigSnapshotMatchesTarget(snapshot, scope.RuntimeType, scope.Profile) {
			continue
		}
		response.Actual = snapshot.Providers
		break
	}
	return response, nil
}

func (h *handler) providerDefinitionMapWithSecretState(ctx context.Context, values map[string][]protocol.ProviderDefinition) (map[string][]protocol.ProviderDefinition, error) {
	if values == nil {
		return nil, nil
	}
	annotated := make(map[string][]protocol.ProviderDefinition, len(values))
	for key, providers := range values {
		next, err := h.providerDefinitionsWithSecretState(ctx, providers)
		if err != nil {
			return nil, err
		}
		annotated[key] = next
	}
	return annotated, nil
}

func (h *handler) providerDefinitionsWithSecretState(ctx context.Context, providers []protocol.ProviderDefinition) ([]protocol.ProviderDefinition, error) {
	if providers == nil {
		return nil, nil
	}
	annotated := make([]protocol.ProviderDefinition, len(providers))
	for i, provider := range providers {
		annotated[i] = provider
		if provider.Models != nil {
			annotated[i].Models = append([]string(nil), provider.Models...)
		}
		managed, err := h.providerSecretManaged(ctx, provider.APIKeyEnv)
		if err != nil {
			return nil, err
		}
		annotated[i].APIKeyManaged = managed
	}
	return annotated, nil
}

func (h *handler) providerSecretManaged(ctx context.Context, envName string) (bool, error) {
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return false, nil
	}
	return h.store.HasProviderSecret(ctx, envName)
}

func providerDefinitionInScope(desired protocol.DesiredConfig, scope spconfig.ProviderScope, name string) (protocol.ProviderDefinition, bool) {
	scope = normalizeHTTPProviderScope(scope)
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return protocol.ProviderDefinition{}, false
	}
	var providers []protocol.ProviderDefinition
	switch {
	case scope.NodeID == "" && scope.RuntimeType == "":
		providers = desired.GlobalProviders
	case scope.RuntimeType == "":
		providers = desired.NodeProviders[scope.NodeID]
	case scope.NodeID == "":
		providers = desired.RuntimeProfileProviders[spconfig.RuntimeProfileKey(scope.RuntimeType, scope.Profile)]
	default:
		providers = desired.NodeRuntimeProfileProviders[spconfig.NodeRuntimeProfileKey(scope.NodeID, scope.RuntimeType, scope.Profile)]
	}
	for _, provider := range providers {
		if strings.ToLower(strings.TrimSpace(provider.Name)) == name {
			return provider, true
		}
	}
	return protocol.ProviderDefinition{}, false
}

func validateProviderDefinitionForUpsert(provider protocol.ProviderDefinition) error {
	return spconfig.ValidateDesiredConfigValues(protocol.DesiredConfig{
		GlobalProviders: []protocol.ProviderDefinition{provider},
	})
}

func providerScopeFromQuery(r *http.Request) spconfig.ProviderScope {
	query := r.URL.Query()
	return normalizeHTTPProviderScope(spconfig.ProviderScope{
		NodeID:      query.Get("nodeId"),
		RuntimeType: query.Get("runtimeType"),
		Profile:     query.Get("profile"),
	})
}

func providerScopeFromProtocol(scope protocol.ProviderScope) spconfig.ProviderScope {
	return normalizeHTTPProviderScope(spconfig.ProviderScope{
		NodeID:      scope.NodeID,
		RuntimeType: scope.RuntimeType,
		Profile:     scope.Profile,
	})
}

func normalizeHTTPProviderScope(scope spconfig.ProviderScope) spconfig.ProviderScope {
	return spconfig.ProviderScope{
		NodeID:      strings.TrimSpace(scope.NodeID),
		RuntimeType: strings.TrimSpace(scope.RuntimeType),
		Profile:     strings.TrimSpace(scope.Profile),
	}
}

func formatProviderScope(scope spconfig.ProviderScope) string {
	scope = normalizeHTTPProviderScope(scope)
	if scope.NodeID == "" && scope.RuntimeType == "" {
		return "global"
	}
	if scope.RuntimeType == "" {
		return "node=" + scope.NodeID
	}
	if scope.NodeID == "" {
		return "runtimeProfile=" + spconfig.RuntimeProfileKey(scope.RuntimeType, scope.Profile)
	}
	return "nodeRuntimeProfile=" + spconfig.NodeRuntimeProfileKey(scope.NodeID, scope.RuntimeType, scope.Profile)
}

func (h *handler) writeEffectiveConfig(w http.ResponseWriter, r *http.Request, nodeID string, runtimeType string, profile string, desired protocol.DesiredConfig) {
	effective := spconfig.EffectiveProviderModelConfig(desired, spconfig.EffectiveConfigTarget{
		NodeID:      nodeID,
		RuntimeType: runtimeType,
		Profile:     profile,
	})
	actual, err := h.latestActualSnapshot(r.Context(), nodeID, runtimeType, profile)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "get actual config")
		return
	}
	diff := spconfig.DiffProviderModelConfig(actual, effective)
	if diff == nil {
		diff = []protocol.ConfigDiffEntry{}
	}
	writeJSON(w, http.StatusOK, protocol.EffectiveConfigResponse{
		NodeID:      nodeID,
		RuntimeType: runtimeType,
		Profile:     profile,
		Effective:   effective,
		DesiredHash: hashDesired(effective),
		Actual:      actual,
		Diff:        diff,
	})
}

func (h *handler) latestActualSnapshot(ctx context.Context, nodeID string, runtimeType string, profile string) (*protocol.RuntimeConfigSnapshot, error) {
	jobs, err := h.store.ListNodeJobs(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if job.Type != protocol.JobTypeDeepProbe || job.Status != protocol.JobStatusCompleted || strings.TrimSpace(job.ResultJSON) == "" {
			continue
		}
		var result protocol.DeepProbeResult
		if err := json.Unmarshal([]byte(job.ResultJSON), &result); err != nil {
			continue
		}
		for _, snapshot := range result.ConfigSnapshots {
			if !runtimeConfigSnapshotMatchesTarget(snapshot, runtimeType, profile) {
				continue
			}
			matched := snapshot
			return &matched, nil
		}
	}
	return nil, nil
}

func runtimeConfigSnapshotMatchesTarget(snapshot protocol.RuntimeConfigSnapshot, runtimeType string, profile string) bool {
	if runtimeType != "" && snapshot.RuntimeType != runtimeType {
		return false
	}
	if profile != "" && snapshot.Profile != "" && snapshot.Profile != profile {
		return false
	}
	return true
}

func hashDesired(effective protocol.ProviderModelConfig) string {
	payload, _ := json.Marshal(effective)
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func hashDesiredConfig(desired protocol.DesiredConfig) string {
	payload, _ := json.Marshal(desired)
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (h *handler) audit(ctx context.Context, event protocol.AuditEvent) {
	if event.Actor == audit.ActorOperator {
		if strings.TrimSpace(event.ActorName) == "" {
			event.ActorName = h.operatorActorName(ctx)
		}
	}
	event.Detail = spconfig.RedactString(event.Detail)
	_, _ = h.store.AppendAuditEvent(ctx, event)
}

func (h *handler) operatorActorName(ctx context.Context) string {
	tokenID := operatorTokenIDFromContext(ctx)
	if tokenID == "" {
		return "bootstrap"
	}
	name, err := h.operatorTokenName(ctx, tokenID)
	if err != nil || strings.TrimSpace(name) == "" {
		return "unknown"
	}
	return name
}

func redactAuditEvents(events []protocol.AuditEvent) []protocol.AuditEvent {
	redacted := make([]protocol.AuditEvent, len(events))
	copy(redacted, events)
	for i := range redacted {
		redacted[i].Detail = spconfig.RedactString(redacted[i].Detail)
	}
	return redacted
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	if message == "" {
		message = http.StatusText(status)
	}
	message = spconfig.RedactString(message)
	code := strings.ToLower(strings.ReplaceAll(http.StatusText(status), " ", "_"))
	writeJSON(w, status, protocol.APIError{
		Code:    code,
		Message: message,
	})
}

func writeJSONDecodeError(w http.ResponseWriter, err error, message string) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeAPIError(w, http.StatusBadRequest, message)
}

type operatorTokenIDContextKey struct{}

// withOperatorTokenID stores the acting named-token id for later audit events.
func withOperatorTokenID(ctx context.Context, tokenID string) context.Context {
	return context.WithValue(ctx, operatorTokenIDContextKey{}, tokenID)
}

func operatorTokenIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(operatorTokenIDContextKey{}).(string)
	return id
}

func isReadOnlyHTTPMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

func (h *handler) authorizeOperatorIdentity(w http.ResponseWriter, r *http.Request) (auth.OperatorIdentity, bool) {
	identity, ok := h.operatorAuth.AuthorizeIdentity(r.Context(), r.Header.Get("Authorization"))
	if !ok {
		if limited, retryAfter := h.operatorAuthLimiter.allow(remoteRateLimitKey(r)); !limited {
			writeRateLimited(w, retryAfter)
			return auth.OperatorIdentity{}, false
		}
		writeAPIError(w, http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized))
		return auth.OperatorIdentity{}, false
	}
	if identity.TokenID != "" {
		*r = *r.WithContext(withOperatorTokenID(r.Context(), identity.TokenID))
	}
	return identity, true
}

func (h *handler) authorizeOperator(w http.ResponseWriter, r *http.Request) bool {
	identity, ok := h.authorizeOperatorIdentity(w, r)
	if !ok {
		return false
	}
	if identity.Scope == protocol.OperatorTokenScopeReadonly && !isReadOnlyHTTPMethod(r.Method) {
		writeAPIError(w, http.StatusForbidden, "operator token is read-only")
		return false
	}
	return true
}

// authorizeOperatorRead gates read-only operator endpoints. Reads require a
// valid operator identity (a read-only token suffices); an explicitly
// unauthenticated deployment (--allow-unauthenticated-operator-api) is still
// permitted via authorizeOperator.
func (h *handler) authorizeOperatorRead(w http.ResponseWriter, r *http.Request) bool {
	return h.authorizeOperator(w, r)
}

func decodeOptionalJSON(body io.Reader, dst any) error {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, limit int64, dst any) error {
	body := http.MaxBytesReader(w, r.Body, limit)
	defer body.Close()

	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

func decodeOptionalJSONRequest(w http.ResponseWriter, r *http.Request, limit int64, dst any) error {
	body := http.MaxBytesReader(w, r.Body, limit)
	defer body.Close()
	return decodeOptionalJSON(body, dst)
}
