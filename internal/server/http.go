package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
)

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
	RateLimits                      RateLimitConfig
	Events                          *EventHub
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

	handler := &handler{
		store:               cfg.Store,
		freshness:           freshness,
		operatorAuth:        auth.NewOperatorTokenWithVerifier(cfg.OperatorToken, cfg.AllowUnauthenticatedOperatorAPI, cfg.Store),
		signingKey:          keyPair,
		events:              cfg.Events,
		eventTickets:        newEventTicketStore(),
		enrollmentLimiter:   newFixedWindowRateLimiter(rateLimits.enrollmentLimit, rateLimits.window, rateLimits.now),
		operatorAuthLimiter: newFixedWindowRateLimiter(rateLimits.operatorAuthLimit, rateLimits.window, rateLimits.now),
		metrics:             NewMetrics(),
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
	mux.HandleFunc("/api/audit", handler.auditEvents)
	mux.HandleFunc("/api/operator-tokens", handler.operatorTokens)
	mux.HandleFunc("/api/operator-tokens/", handler.operatorTokenRouter)
	mux.HandleFunc("/api/signing-key", handler.publicSigningKey)
	mux.HandleFunc("/api/config/desired/history", handler.desiredConfigHistory)
	mux.HandleFunc("/api/config/desired/revert", handler.revertDesiredConfig)
	mux.HandleFunc("/api/config/desired", handler.desiredConfig)
	mux.HandleFunc("/api/config/effective", handler.effectiveConfig)
	mux.HandleFunc("/api/config/effective/preview", handler.previewEffectiveConfig)
	mux.HandleFunc("/api/enrollment-tokens", handler.createEnrollmentToken)
	mux.HandleFunc("/api/enroll", handler.enrollNode)
	mux.HandleFunc("/api/heartbeat", handler.heartbeat)
	mux.HandleFunc("/api/rollouts", handler.rollouts)
	mux.HandleFunc("/api/rollouts/", handler.rolloutRouter)
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
	events              *EventHub
	eventTickets        *eventTicketStore
	enrollmentLimiter   *fixedWindowRateLimiter
	operatorAuthLimiter *fixedWindowRateLimiter
	metrics             *Metrics
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
	nodesByState map[protocol.NodeState]int
	driftedNodes int
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

	snapshot := fleetMetricsSnapshot{
		nodesByState: map[protocol.NodeState]int{
			protocol.NodeStateFresh:   0,
			protocol.NodeStateStale:   0,
			protocol.NodeStateOffline: 0,
		},
	}
	for _, node := range nodes {
		snapshot.nodesByState[node.State]++
		drift, err := h.nodeHasConfigDrift(ctx, node.NodeID, desired)
		if err != nil {
			return fleetMetricsSnapshot{}, err
		}
		if drift {
			snapshot.driftedNodes++
		}
	}
	return snapshot, nil
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

	response := make([]protocol.NodeStatusWithDrift, len(nodeList.Nodes))
	for i, node := range nodeList.Nodes {
		drift, err := h.nodeHasConfigDrift(r.Context(), node.NodeID, desired)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "get actual config")
			return
		}
		response[i] = protocol.NodeStatusWithDrift{
			NodeStatus: node,
			Drift:      drift,
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
	spec, err := normalizeRolloutSpec(req.Spec)
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
	spec.NodeIDs = nodeIDs
	spec.Selector = cloneStringMap(spec.Selector)
	now := time.Now().UTC()
	created, err := h.store.CreateRollout(r.Context(), protocol.Rollout{
		Spec:      spec,
		State:     protocol.RolloutStatePending,
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
	publishRolloutEvent(h.events, created)
	writeJSON(w, http.StatusCreated, protocol.CreateRolloutResponse{Rollout: created})
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
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:     audit.ActorOperator,
		Action:    action,
		Detail:    "rollout=" + rollout.ID,
		CreatedAt: now,
	})
	publishRolloutEvent(h.events, *rollout)
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
	return nil
}

func (h *handler) resolveRolloutNodes(ctx context.Context, spec protocol.RolloutSpec) ([]string, error) {
	if len(spec.NodeIDs) > 0 {
		for _, nodeID := range spec.NodeIDs {
			exists, err := h.store.NodeExists(ctx, nodeID)
			if err != nil {
				return nil, err
			}
			if !exists {
				return nil, store.ErrNodeNotFound
			}
		}
		return append([]string(nil), spec.NodeIDs...), nil
	}
	list, err := h.store.ListNodesFiltered(ctx, store.NodeFilter{
		Labels: spec.Selector,
		Limit:  store.MaxNodeListLimit,
	})
	if err != nil {
		return nil, err
	}
	nodeIDs := make([]string, 0, len(list.Nodes))
	for _, node := range list.Nodes {
		nodeIDs = append(nodeIDs, node.NodeID)
	}
	return nodeIDs, nil
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

func (h *handler) nodeHasConfigDrift(ctx context.Context, nodeID string, desired protocol.DesiredConfig) (bool, error) {
	effective := spconfig.EffectiveProviderModelConfig(desired, spconfig.EffectiveConfigTarget{NodeID: nodeID})
	if !hasKnownProviderModel(effective) {
		return false, nil
	}

	actual, err := h.latestActualSnapshot(ctx, nodeID, "", "")
	if err != nil {
		return false, err
	}
	if actual == nil || !hasKnownProviderModel(protocol.ProviderModelConfig{
		Provider: actual.Provider,
		Model:    actual.Model,
	}) {
		return false, nil
	}

	for _, entry := range spconfig.DiffProviderModelConfig(actual, effective) {
		if entry.Change == protocol.ConfigDiffChangeUpdate &&
			strings.TrimSpace(entry.Actual) != "" &&
			strings.TrimSpace(entry.Desired) != "" {
			return true, nil
		}
	}
	return false, nil
}

func hasKnownProviderModel(value protocol.ProviderModelConfig) bool {
	return strings.TrimSpace(value.Provider) != "" && strings.TrimSpace(value.Model) != ""
}

// nodeJobsRouter handles node-scoped API routes under /api/nodes/{nodeId}.
func (h *handler) nodeJobsRouter(w http.ResponseWriter, r *http.Request) {
	// Parse path: /api/nodes/{nodeId}/{labels|backups|jobs|config-apply|restart|rollback}
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
	effective := spconfig.EffectiveProviderModelConfig(desired, spconfig.EffectiveConfigTarget{
		NodeID:      nodeID,
		RuntimeType: runtimeType,
		Profile:     profile,
	})
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
			Profile: actual.ConfigPath,
			Desired: effective,
			DryRun:  dryRun,
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
	writeJSON(w, http.StatusOK, protocol.EffectiveConfigResponse{
		NodeID:      nodeID,
		RuntimeType: runtimeType,
		Profile:     profile,
		Effective:   effective,
		DesiredHash: hashDesired(effective),
		Actual:      actual,
		Diff:        spconfig.DiffProviderModelConfig(actual, effective),
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
			if runtimeType != "" && snapshot.RuntimeType != runtimeType {
				continue
			}
			if profile != "" && snapshot.Profile != profile {
				continue
			}
			matched := snapshot
			return &matched, nil
		}
	}
	return nil, nil
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
		// Attribute the acting named token by its non-secret id. The key
		// avoids the secret-redaction keywords (token/secret/etc.) so the
		// attribution survives RedactString below.
		if tokenID := operatorTokenIDFromContext(ctx); tokenID != "" {
			if strings.TrimSpace(event.Detail) == "" {
				event.Detail = "actor_id=" + tokenID
			} else {
				event.Detail = event.Detail + " actor_id=" + tokenID
			}
		}
	}
	event.Detail = spconfig.RedactString(event.Detail)
	_, _ = h.store.AppendAuditEvent(ctx, event)
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

func (h *handler) authorizeOperator(w http.ResponseWriter, r *http.Request) bool {
	identity, ok := h.operatorAuth.AuthorizeIdentity(r.Context(), r.Header.Get("Authorization"))
	if !ok {
		if limited, retryAfter := h.operatorAuthLimiter.allow(remoteRateLimitKey(r)); !limited {
			writeRateLimited(w, retryAfter)
			return false
		}
		writeAPIError(w, http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized))
		return false
	}
	if identity.TokenID != "" {
		*r = *r.WithContext(withOperatorTokenID(r.Context(), identity.TokenID))
	}
	if identity.Scope == protocol.OperatorTokenScopeReadonly && !isReadOnlyHTTPMethod(r.Method) {
		writeAPIError(w, http.StatusForbidden, "operator token is read-only")
		return false
	}
	return true
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
