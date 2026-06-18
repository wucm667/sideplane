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
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wucm667/sideplane/internal/audit"
	"github.com/wucm667/sideplane/internal/auth"
	"github.com/wucm667/sideplane/internal/store"
	spconfig "github.com/wucm667/sideplane/pkg/config"
	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	"github.com/wucm667/sideplane/pkg/protocol"
)

const defaultEnrollmentTokenTTL = time.Hour

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

	handler := &handler{
		store:        cfg.Store,
		freshness:    freshness,
		operatorAuth: auth.NewOperatorToken(cfg.OperatorToken, cfg.AllowUnauthenticatedOperatorAPI),
		signingKey:   keyPair,
		metrics:      NewMetrics(),
		timedOutJobs: map[string]struct{}{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", jsonStatusHandler("ok"))
	mux.HandleFunc("/readyz", jsonStatusHandler("ready"))
	mux.HandleFunc("/metrics", handler.metricsEndpoint)
	mux.HandleFunc("/api/audit", handler.auditEvents)
	mux.HandleFunc("/api/signing-key", handler.publicSigningKey)
	mux.HandleFunc("/api/config/desired", handler.desiredConfig)
	mux.HandleFunc("/api/config/effective", handler.effectiveConfig)
	mux.HandleFunc("/api/config/effective/preview", handler.previewEffectiveConfig)
	mux.HandleFunc("/api/enrollment-tokens", handler.createEnrollmentToken)
	mux.HandleFunc("/api/enroll", handler.enrollNode)
	mux.HandleFunc("/api/heartbeat", handler.heartbeat)
	mux.HandleFunc("/api/nodes", handler.nodes)
	mux.HandleFunc("/api/nodes/", handler.nodeJobsRouter)
	mux.HandleFunc("/api/sidecar/jobs/next", handler.claimNextJob)
	mux.HandleFunc("/api/sidecar/jobs/", handler.submitJobResult)
	return securityHeaders(mux), nil
}

type handler struct {
	store        store.Store
	freshness    FreshnessPolicy
	operatorAuth auth.OperatorToken
	signingKey   spcrypto.KeyPair
	metrics      *Metrics
	timedOutMu   sync.Mutex
	timedOutJobs map[string]struct{}
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
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	if !h.authorizeOperator(w, r) {
		return
	}
	defer r.Body.Close()

	var req protocol.CreateEnrollmentTokenRequest
	if err := decodeOptionalJSON(r.Body, &req); err != nil {
		http.Error(w, "invalid enrollment token JSON", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	expiresAt := req.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = now.Add(defaultEnrollmentTokenTTL)
	}
	if !expiresAt.After(now) {
		http.Error(w, "expiresAt must be in the future", http.StatusBadRequest)
		return
	}

	resp, err := h.store.CreateEnrollmentToken(r.Context(), expiresAt, now)
	if err != nil {
		http.Error(w, "create enrollment token", http.StatusInternalServerError)
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

func (h *handler) enrollNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	defer r.Body.Close()

	var req protocol.EnrollNodeRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid enroll JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}

	resp, err := h.store.EnrollNode(r.Context(), req, time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, store.ErrEnrollmentTokenInvalid),
			errors.Is(err, store.ErrEnrollmentTokenExpired),
			errors.Is(err, store.ErrEnrollmentTokenUsed):
			http.Error(w, "enrollment token rejected", http.StatusUnauthorized)
		case errors.Is(err, store.ErrNodeAlreadyEnrolled):
			http.Error(w, "node is already enrolled", http.StatusConflict)
		default:
			http.Error(w, "enroll node", http.StatusInternalServerError)
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
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	defer r.Body.Close()

	var req protocol.HeartbeatRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid heartbeat JSON", http.StatusBadRequest)
		return
	}

	req.NodeID = strings.TrimSpace(req.NodeID)
	if req.NodeID == "" {
		http.Error(w, "nodeId is required", http.StatusBadRequest)
		return
	}

	credential, ok := auth.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	ok, err := h.store.VerifyNodeCredential(r.Context(), req.NodeID, credential)
	if err != nil {
		http.Error(w, "verify node credential", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	now := time.Now().UTC()
	node, err := h.store.RecordHeartbeat(r.Context(), req, now)
	if err != nil {
		http.Error(w, "record heartbeat", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, protocol.HeartbeatResponse{
		Accepted:   true,
		ServerTime: now,
		Node:       node,
	})
}

func (h *handler) nodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	nodes, err := h.store.ListNodes(r.Context())
	if err != nil {
		http.Error(w, "list nodes", http.StatusInternalServerError)
		return
	}
	h.applyFreshness(nodes)

	desired, err := h.store.GetDesiredConfig(r.Context())
	if err != nil {
		http.Error(w, "get desired config", http.StatusInternalServerError)
		return
	}

	response := make([]nodeStatusResponse, len(nodes))
	for i, node := range nodes {
		drift, err := h.nodeHasConfigDrift(r.Context(), node.NodeID, desired)
		if err != nil {
			http.Error(w, "get actual config", http.StatusInternalServerError)
			return
		}
		response[i] = nodeStatusResponse{
			NodeStatus: node,
			Drift:      drift,
		}
	}

	writeJSON(w, http.StatusOK, response)
}

type nodeStatusResponse struct {
	protocol.NodeStatus
	Drift bool `json:"drift"`
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

// nodeJobsRouter handles GET and POST /api/nodes/{nodeId}/jobs
func (h *handler) nodeJobsRouter(w http.ResponseWriter, r *http.Request) {
	// Parse path: /api/nodes/{nodeId}/{jobs|config-apply}
	path := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	nodeID := strings.TrimSpace(parts[0])
	if nodeID == "" {
		http.NotFound(w, r)
		return
	}

	switch parts[1] {
	case "jobs":
		switch r.Method {
		case http.MethodGet:
			h.listNodeJobs(w, r, nodeID)
		case http.MethodPost:
			h.createNodeJob(w, r, nodeID)
		default:
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		}
	case "config-apply":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		h.createConfigApplyJob(w, r, nodeID)
	default:
		http.NotFound(w, r)
	}
}

func (h *handler) listNodeJobs(w http.ResponseWriter, r *http.Request, nodeID string) {
	jobs, err := h.store.ListNodeJobs(r.Context(), nodeID)
	if err != nil {
		http.Error(w, "list node jobs", http.StatusInternalServerError)
		return
	}
	h.observeTimedOutJobs(r.Context(), jobs)
	if h.shouldHideJobResults(r) {
		jobs = summarizeJobs(jobs)
	}

	writeJSON(w, http.StatusOK, jobs)
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

func (h *handler) createNodeJob(w http.ResponseWriter, r *http.Request, nodeID string) {
	if !h.authorizeOperator(w, r) {
		return
	}
	defer r.Body.Close()

	var req protocol.CreateJobRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid job JSON", http.StatusBadRequest)
		return
	}

	if req.Type == "" {
		http.Error(w, "type is required", http.StatusBadRequest)
		return
	}
	if req.Type != protocol.JobTypeDeepProbe {
		http.Error(w, "unsupported job type", http.StatusBadRequest)
		return
	}

	exists, err := h.store.NodeExists(r.Context(), nodeID)
	if err != nil {
		http.Error(w, "lookup node", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}

	job, err := h.store.CreateJob(r.Context(), req, nodeID, time.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrActiveJobExists) {
			http.Error(w, "active job already exists", http.StatusConflict)
			return
		}
		http.Error(w, "create job", http.StatusInternalServerError)
		return
	}
	h.metrics.IncJobCreated(string(req.Type))
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:      audit.ActorOperator,
		Action:     audit.ActionJobCreate,
		TargetNode: nodeID,
		Detail:     string(req.Type),
		CreatedAt:  job.CreatedAt,
	})

	writeJSON(w, http.StatusCreated, job)
}

// createConfigApplyJob builds a signed config plan from the desired config and
// enqueues a config_apply job for the node. It defaults to a dry-run plan.
func (h *handler) createConfigApplyJob(w http.ResponseWriter, r *http.Request, nodeID string) {
	if !h.authorizeOperator(w, r) {
		return
	}
	defer r.Body.Close()

	var req protocol.ConfigApplyRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid config apply JSON", http.StatusBadRequest)
		return
	}

	runtimeType := strings.TrimSpace(req.RuntimeType)
	if runtimeType == "" {
		runtimeType = "hermes"
	}
	if runtimeType != "hermes" {
		http.Error(w, "unsupported runtime type", http.StatusBadRequest)
		return
	}
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	profile := strings.TrimSpace(req.Profile)

	if len(h.signingKey.PrivateKey) == 0 {
		http.Error(w, "server signing key is not configured", http.StatusServiceUnavailable)
		return
	}

	exists, err := h.store.NodeExists(r.Context(), nodeID)
	if err != nil {
		http.Error(w, "lookup node", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}

	desired, err := h.store.GetDesiredConfig(r.Context())
	if err != nil {
		http.Error(w, "get desired config", http.StatusInternalServerError)
		return
	}
	effective := spconfig.EffectiveProviderModelConfig(desired, spconfig.EffectiveConfigTarget{
		NodeID:      nodeID,
		RuntimeType: runtimeType,
		Profile:     profile,
	})
	if strings.TrimSpace(effective.Provider) == "" || strings.TrimSpace(effective.Model) == "" {
		http.Error(w, "desired provider and model must be set before applying config", http.StatusBadRequest)
		return
	}
	if err := spconfig.ValidateProviderModelSelection(effective); err != nil {
		http.Error(w, "invalid desired provider/model: "+err.Error(), http.StatusBadRequest)
		return
	}

	actual, err := h.latestActualSnapshot(r.Context(), nodeID, runtimeType, profile)
	if err != nil {
		http.Error(w, "get actual config", http.StatusInternalServerError)
		return
	}
	if actual == nil || strings.TrimSpace(actual.ConfigPath) == "" {
		http.Error(w, "no known config path for node; run a deep probe first", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	planID, err := newPlanID()
	if err != nil {
		http.Error(w, "generate plan id", http.StatusInternalServerError)
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
		http.Error(w, "sign config plan", http.StatusInternalServerError)
		return
	}
	payload, err := json.Marshal(signed)
	if err != nil {
		http.Error(w, "marshal signed plan", http.StatusInternalServerError)
		return
	}

	job, err := h.store.CreateJob(r.Context(), protocol.CreateJobRequest{
		Type:        protocol.JobTypeConfigApply,
		PayloadJSON: string(payload),
	}, nodeID, now)
	if err != nil {
		if errors.Is(err, store.ErrActiveJobExists) {
			http.Error(w, "active config_apply job already exists", http.StatusConflict)
			return
		}
		http.Error(w, "create job", http.StatusInternalServerError)
		return
	}
	h.metrics.IncJobCreated(string(protocol.JobTypeConfigApply))
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:      audit.ActorOperator,
		Action:     audit.ActionConfigApply,
		TargetNode: nodeID,
		Detail:     fmt.Sprintf("%s %s plan=%s", runtimeType, mode, planID),
		CreatedAt:  job.CreatedAt,
	})

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
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	nodeID := strings.TrimSpace(r.URL.Query().Get("nodeId"))
	if nodeID == "" {
		http.Error(w, "nodeId query parameter is required", http.StatusBadRequest)
		return
	}

	credential, ok := auth.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	ok, err := h.store.VerifyNodeCredential(r.Context(), nodeID, credential)
	if err != nil {
		http.Error(w, "verify node credential", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	job, err := h.store.ClaimNextJob(r.Context(), nodeID, time.Now().UTC())
	if err != nil {
		http.Error(w, "claim next job", http.StatusInternalServerError)
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

	writeJSON(w, http.StatusOK, job)
}

// submitJobResult handles POST /api/sidecar/jobs/{jobId}/result
func (h *handler) submitJobResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	// Parse path: /api/sidecar/jobs/{jobId}/result
	path := strings.TrimPrefix(r.URL.Path, "/api/sidecar/jobs/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "result" {
		http.NotFound(w, r)
		return
	}
	jobID := strings.TrimSpace(parts[0])
	if jobID == "" {
		http.NotFound(w, r)
		return
	}

	credential, ok := auth.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	// Load job to get nodeId for credential verification
	job, err := h.store.GetJob(r.Context(), jobID)
	if err != nil {
		http.Error(w, "get job", http.StatusInternalServerError)
		return
	}
	if job == nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	ok, err = h.store.VerifyNodeCredential(r.Context(), job.NodeID, credential)
	if err != nil {
		http.Error(w, "verify node credential", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	defer r.Body.Close()

	var req protocol.JobResultRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid result JSON", http.StatusBadRequest)
		return
	}

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
		http.Error(w, "status must be completed or failed", http.StatusBadRequest)
		return
	}

	if err != nil {
		if errors.Is(err, store.ErrLateJobResultRecorded) {
			lateResult = true
		} else {
			http.Error(w, "submit job result", http.StatusInternalServerError)
			return
		}
	}
	if lateResult {
		h.audit(r.Context(), protocol.AuditEvent{
			Actor:      audit.ActorSidecar,
			Action:     audit.ActionJobFail,
			TargetNode: job.NodeID,
			Detail:     string(job.Type) + " late_result_after_timeout",
			CreatedAt:  now,
		})
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
	h.audit(r.Context(), protocol.AuditEvent{
		Actor:      audit.ActorSidecar,
		Action:     action,
		TargetNode: job.NodeID,
		Detail:     string(job.Type),
		CreatedAt:  now,
	})

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
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	events, err := h.store.ListAuditEvents(r.Context(), 100)
	if err != nil {
		http.Error(w, "list audit events", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, protocol.ListAuditEventsResponse{Events: events})
}

func (h *handler) publicSigningKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if len(h.signingKey.PublicKey) == 0 {
		http.Error(w, "signing key unavailable", http.StatusServiceUnavailable)
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
			http.Error(w, "get desired config", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, desired)
	case http.MethodPut:
		if !h.authorizeOperator(w, r) {
			return
		}
		defer r.Body.Close()
		var desired protocol.DesiredConfig
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&desired); err != nil {
			http.Error(w, "invalid desired config JSON", http.StatusBadRequest)
			return
		}
		if err := spconfig.ValidateDesiredConfigValues(desired); err != nil {
			http.Error(w, "invalid desired config: "+err.Error(), http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		if err := h.store.SetDesiredConfig(r.Context(), desired, now); err != nil {
			http.Error(w, "set desired config", http.StatusInternalServerError)
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
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

func (h *handler) effectiveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	nodeID := strings.TrimSpace(r.URL.Query().Get("nodeId"))
	if nodeID == "" {
		http.Error(w, "nodeId query parameter is required", http.StatusBadRequest)
		return
	}
	runtimeType := strings.TrimSpace(r.URL.Query().Get("runtimeType"))
	profile := strings.TrimSpace(r.URL.Query().Get("profile"))

	desired, err := h.store.GetDesiredConfig(r.Context())
	if err != nil {
		http.Error(w, "get desired config", http.StatusInternalServerError)
		return
	}
	h.writeEffectiveConfig(w, r, nodeID, runtimeType, profile, desired)
}

func (h *handler) previewEffectiveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if !h.authorizeOperator(w, r) {
		return
	}
	defer r.Body.Close()

	var req protocol.EffectiveConfigPreviewRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid config preview JSON", http.StatusBadRequest)
		return
	}
	if err := spconfig.ValidateProviderModelSelection(req.Desired); err != nil {
		http.Error(w, "invalid desired provider/model: "+err.Error(), http.StatusBadRequest)
		return
	}

	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		http.Error(w, "nodeId is required", http.StatusBadRequest)
		return
	}
	runtimeType := strings.TrimSpace(req.RuntimeType)
	profile := strings.TrimSpace(req.Profile)

	desired, err := h.store.GetDesiredConfig(r.Context())
	if err != nil {
		http.Error(w, "get desired config", http.StatusInternalServerError)
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
		http.Error(w, "get actual config", http.StatusInternalServerError)
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
	_, _ = h.store.AppendAuditEvent(ctx, event)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func (h *handler) authorizeOperator(w http.ResponseWriter, r *http.Request) bool {
	if h.operatorAuth.AuthorizeHeader(r.Header.Get("Authorization")) {
		return true
	}
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
	return false
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
