package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wucm667/sideplane/internal/store"
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
	if freshness.Now == nil {
		freshness.Now = utcNow
	}
	if err := freshness.Validate(); err != nil {
		return nil, err
	}

	handler := &handler{
		store:     nodeStore,
		freshness: freshness,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", jsonStatusHandler("ok"))
	mux.HandleFunc("/readyz", jsonStatusHandler("ready"))
	mux.HandleFunc("/metrics", metricsHandler)
	mux.HandleFunc("/api/enrollment-tokens", handler.createEnrollmentToken)
	mux.HandleFunc("/api/enroll", handler.enrollNode)
	mux.HandleFunc("/api/heartbeat", handler.heartbeat)
	mux.HandleFunc("/api/nodes", handler.nodes)
	mux.HandleFunc("/api/nodes/", handler.nodeJobsRouter)
	mux.HandleFunc("/api/sidecar/jobs/next", handler.claimNextJob)
	mux.HandleFunc("/api/sidecar/jobs/", handler.submitJobResult)
	return mux, nil
}

type handler struct {
	store     store.Store
	freshness FreshnessPolicy
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

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte("# Sideplane metrics placeholder\n"))
}

func (h *handler) createEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	// TODO(auth): require an authenticated operator before issuing enrollment tokens.
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

	credential, ok := bearerCredential(r.Header.Get("Authorization"))
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

	writeJSON(w, http.StatusOK, nodes)
}

func (h *handler) applyFreshness(nodes []protocol.NodeStatus) {
	for i := range nodes {
		nodes[i].State = h.freshness.StateFor(nodes[i].LastHeartbeatAt)
	}
}

// nodeJobsRouter handles GET and POST /api/nodes/{nodeId}/jobs
func (h *handler) nodeJobsRouter(w http.ResponseWriter, r *http.Request) {
	// Parse path: /api/nodes/{nodeId}/jobs
	path := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "jobs" {
		http.NotFound(w, r)
		return
	}
	nodeID := strings.TrimSpace(parts[0])
	if nodeID == "" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.listNodeJobs(w, r, nodeID)
	case http.MethodPost:
		h.createNodeJob(w, r, nodeID)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

func (h *handler) listNodeJobs(w http.ResponseWriter, r *http.Request, nodeID string) {
	jobs, err := h.store.ListNodeJobs(r.Context(), nodeID)
	if err != nil {
		http.Error(w, "list node jobs", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, jobs)
}

func (h *handler) createNodeJob(w http.ResponseWriter, r *http.Request, nodeID string) {
	// TODO(auth): require operator authentication before creating jobs
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

	writeJSON(w, http.StatusCreated, job)
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

	credential, ok := bearerCredential(r.Header.Get("Authorization"))
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

	credential, ok := bearerCredential(r.Header.Get("Authorization"))
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
	if req.Status == protocol.JobStatusCompleted {
		err = h.store.CompleteJob(r.Context(), jobID, req, now)
	} else if req.Status == protocol.JobStatusFailed {
		err = h.store.FailJob(r.Context(), jobID, req.Error, now)
	} else {
		http.Error(w, "status must be completed or failed", http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, "submit job result", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func bearerCredential(authorization string) (string, bool) {
	fields := strings.Fields(authorization)
	if len(fields) != 2 {
		return "", false
	}
	if !strings.EqualFold(fields[0], "Bearer") {
		return "", false
	}
	credential := strings.TrimSpace(fields[1])
	if credential == "" {
		return "", false
	}
	return credential, true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
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
