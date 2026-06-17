package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wucm667/sideplane/internal/store"
	spcrypto "github.com/wucm667/sideplane/pkg/crypto"
	"github.com/wucm667/sideplane/pkg/protocol"
)

func TestCreateJobAPI(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	body := strings.NewReader(`{"type":"deep_probe","payloadJson":"{}"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", body)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusCreated)

	var job protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	if job.NodeID != "node-jobs" {
		t.Fatalf("job nodeId = %q, want node-jobs", job.NodeID)
	}
	if job.Type != protocol.JobTypeDeepProbe {
		t.Fatalf("job type = %q, want deep_probe", job.Type)
	}
	if job.Status != protocol.JobStatusPending {
		t.Fatalf("job status = %q, want pending", job.Status)
	}
}

func TestCreateJobAPIAllowsLocalDevWhenOperatorTokenNotConfigured(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"deep_probe"}`))

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusCreated)
}

func TestCreateJobAPIRequiresConfiguredOperatorToken(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{
			name:       "missing",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:          "wrong",
			authorization: "Bearer wrong-token",
			wantStatus:    http.StatusUnauthorized,
		},
		{
			name:          "correct",
			authorization: "Bearer dev-token",
			wantStatus:    http.StatusCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeStore := store.NewMemoryNodeStore()
			enrollTestNode(t, nodeStore, "node-jobs")

			handler, err := NewHandlerWithStoreAndFreshnessPolicyAndOperatorToken(nodeStore, DefaultFreshnessPolicy(), "dev-token")
			if err != nil {
				t.Fatalf("build handler: %v", err)
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"deep_probe"}`))
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}

			handler.ServeHTTP(rec, req)

			assertStatus(t, rec, tt.wantStatus)
		})
	}
}

func TestCreateJobAPIRejectsMalformedJSON(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":`))

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusBadRequest)
}

func TestCreateJobAPIRejectsUnsupportedType(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"bad"}`))

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusBadRequest)
}

func TestCreateJobAPIRejectsUnknownNode(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/missing-node/jobs", strings.NewReader(`{"type":"deep_probe"}`))

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusNotFound)
}

func TestCreateJobAPIRejectsDuplicatePendingDeepProbe(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	handler := NewHandlerWithStore(nodeStore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"deep_probe"}`))

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusCreated)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"deep_probe"}`))

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusConflict)
}

func TestCreateJobAPIRejectsDuplicateClaimedDeepProbe(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	if _, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", time.Now().UTC()); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-jobs", time.Now().UTC()); err != nil {
		t.Fatalf("claim job: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-jobs/jobs", strings.NewReader(`{"type":"deep_probe"}`))

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusConflict)
}

func TestListNodeJobsAPI(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")
	enrollTestNode(t, nodeStore, "other-node")

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	older, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", now)
	if err != nil {
		t.Fatalf("create older job: %v", err)
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-jobs", now.Add(30*time.Second)); err != nil {
		t.Fatalf("claim older job: %v", err)
	}
	if err := nodeStore.CompleteJob(context.Background(), older.ID, protocol.JobResultRequest{Status: protocol.JobStatusCompleted}, now.Add(45*time.Second)); err != nil {
		t.Fatalf("complete older job: %v", err)
	}
	newer, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("create newer job: %v", err)
	}
	if _, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "other-node", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("create other job: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-jobs/jobs", nil)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var jobs []protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode jobs response: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("jobs length = %d, want 2", len(jobs))
	}
	if jobs[0].ID != newer.ID || jobs[1].ID != older.ID {
		t.Fatalf("jobs order = [%q, %q], want [%q, %q]", jobs[0].ID, jobs[1].ID, newer.ID, older.ID)
	}
}

func TestListNodeJobsAPIOmitsUnsetTimestamps(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-jobs")

	if _, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", time.Now().UTC()); err != nil {
		t.Fatalf("create pending job: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-jobs/jobs", nil)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var jobs []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode jobs response: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs length = %d, want 1", len(jobs))
	}
	if _, ok := jobs[0]["claimedAt"]; ok {
		t.Fatalf("pending job response includes claimedAt: %#v", jobs[0])
	}
	if _, ok := jobs[0]["finishedAt"]; ok {
		t.Fatalf("pending job response includes finishedAt: %#v", jobs[0])
	}
}

func TestListNodeJobsAPIIncludesFinishedTimestamps(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-jobs")

	job, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-jobs", time.Now().UTC())
	if err != nil {
		t.Fatalf("create pending job: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sidecar/jobs/next?nodeId=node-jobs", nil)
	req.Header.Set("Authorization", "Bearer "+credential)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/sidecar/jobs/"+job.ID+"/result", strings.NewReader(`{"status":"completed","resultJson":"{}"}`))
	req.Header.Set("Authorization", "Bearer "+credential)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes/node-jobs/jobs", nil)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var jobs []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode jobs response: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs length = %d, want 1", len(jobs))
	}
	if _, ok := jobs[0]["claimedAt"]; !ok {
		t.Fatalf("completed job response omits claimedAt: %#v", jobs[0])
	}
	if _, ok := jobs[0]["finishedAt"]; !ok {
		t.Fatalf("completed job response omits finishedAt: %#v", jobs[0])
	}
}

func TestAuditAPIRecordsFleetOperations(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	handler := NewHandlerWithStore(nodeStore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/enrollment-tokens", strings.NewReader(`{}`))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	var tokenResp protocol.CreateEnrollmentTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode token response: %v", err)
	}

	var enrollBody bytes.Buffer
	if err := json.NewEncoder(&enrollBody).Encode(protocol.EnrollNodeRequest{
		Token:  tokenResp.Token,
		NodeID: "node-audit",
	}); err != nil {
		t.Fatalf("encode enroll request: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/enroll", &enrollBody)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	var enrollResp protocol.EnrollNodeResponse
	if err := json.NewDecoder(rec.Body).Decode(&enrollResp); err != nil {
		t.Fatalf("decode enroll response: %v", err)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/nodes/node-audit/jobs", strings.NewReader(`{"type":"deep_probe"}`))
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	var job protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/sidecar/jobs/next?nodeId=node-audit", nil)
	req.Header.Set("Authorization", "Bearer "+enrollResp.NodeCredential)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/sidecar/jobs/"+job.ID+"/result", strings.NewReader(`{"status":"completed","resultJson":"{}"}`))
	req.Header.Set("Authorization", "Bearer "+enrollResp.NodeCredential)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	var resp protocol.ListAuditEventsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(resp.Events) != 4 {
		t.Fatalf("audit event count = %d, want 4: %#v", len(resp.Events), resp.Events)
	}
	gotActions := map[string]bool{}
	for _, event := range resp.Events {
		gotActions[event.Action] = true
		payload, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal audit event: %v", err)
		}
		if strings.Contains(string(payload), tokenResp.Token) || strings.Contains(string(payload), enrollResp.NodeCredential) {
			t.Fatalf("audit event leaked credential material: %s", payload)
		}
	}
	for _, action := range []string{"enrollment.token.create", "node.enroll", "job.create", "job.complete"} {
		if !gotActions[action] {
			t.Fatalf("missing audit action %q in %#v", action, resp.Events)
		}
	}
}

func TestListNodeJobsAPISurfacesTimedOutJob(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-timeout")

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	job, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-timeout", now)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	claimed, err := nodeStore.ClaimNextJob(context.Background(), "node-timeout", now.Add(time.Second))
	if err != nil {
		t.Fatalf("claim job: %v", err)
	}
	if claimed == nil {
		t.Fatalf("claimed job is nil")
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-timeout", claimed.ClaimExpiresAt.Add(time.Second)); err != nil {
		t.Fatalf("advance timeout: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-timeout/jobs", nil)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var jobs []protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode jobs response: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs length = %d, want 1", len(jobs))
	}
	if jobs[0].ID != job.ID {
		t.Fatalf("job ID = %q, want %q", jobs[0].ID, job.ID)
	}
	if jobs[0].Status != protocol.JobStatusFailed {
		t.Fatalf("job status = %q, want failed", jobs[0].Status)
	}
	if jobs[0].Error != "job claim timed out" {
		t.Fatalf("job error = %q, want timeout error", jobs[0].Error)
	}
}

func TestSidecarClaimsOnlyOwnNodeJobs(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	nodeACredential := enrollTestNode(t, nodeStore, "node-a")
	enrollTestNode(t, nodeStore, "node-b")

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if _, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-b", now); err != nil {
		t.Fatalf("create node-b job: %v", err)
	}
	jobA, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-a", now.Add(time.Second))
	if err != nil {
		t.Fatalf("create node-a job: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sidecar/jobs/next?nodeId=node-a", nil)
	req.Header.Set("Authorization", "Bearer "+nodeACredential)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var job protocol.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	if job.ID != jobA.ID {
		t.Fatalf("claimed job = %q, want node-a job %q", job.ID, jobA.ID)
	}
	if job.NodeID != "node-a" {
		t.Fatalf("claimed nodeId = %q, want node-a", job.NodeID)
	}
}

func TestSidecarJobPollingRejectsWrongCredential(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-auth")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sidecar/jobs/next?nodeId=node-auth", nil)
	req.Header.Set("Authorization", "Bearer wrong-credential")

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestSidecarJobResultRejectsWrongCredential(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-result")
	job, err := nodeStore.CreateJob(context.Background(), protocol.CreateJobRequest{Type: protocol.JobTypeDeepProbe}, "node-result", time.Now().UTC())
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := nodeStore.ClaimNextJob(context.Background(), "node-result", time.Now().UTC()); err != nil {
		t.Fatalf("claim job: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/sidecar/jobs/"+job.ID+"/result", strings.NewReader(`{"status":"completed","resultJson":"{}"}`))
	req.Header.Set("Authorization", "Bearer wrong-credential")

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)
	assertJSONStatus(t, rec, "ok")
}

func TestReadyz(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)
	assertJSONStatus(t, rec, "ready")
}

func TestMetricsPlaceholder(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	if got := rec.Body.String(); !strings.Contains(got, "Sideplane metrics placeholder") {
		t.Fatalf("metrics body = %q, want placeholder text", got)
	}
}

func TestPublicSigningKeyAPI(t *testing.T) {
	keyPair, err := spcrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:          store.NewMemoryNodeStore(),
		Freshness:      DefaultFreshnessPolicy(),
		SigningKeyPair: keyPair,
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/signing-key", nil)
	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)
	var resp protocol.PublicSigningKeyResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode signing key response: %v", err)
	}
	if resp.Algorithm != "ed25519" {
		t.Fatalf("algorithm = %q, want ed25519", resp.Algorithm)
	}
	if resp.PublicKey != spcrypto.PublicKeyString(keyPair.PublicKey) {
		t.Fatalf("public key response mismatch")
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if strings.Contains(string(payload), "private") {
		t.Fatalf("signing key response mentions private key: %s", payload)
	}
}

func TestHandlerLoadsPersistedSigningKey(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "signing-key.json")
	first, err := NewHandlerWithConfig(HandlerConfig{
		Store:          store.NewMemoryNodeStore(),
		Freshness:      DefaultFreshnessPolicy(),
		SigningKeyPath: keyPath,
	})
	if err != nil {
		t.Fatalf("build first handler: %v", err)
	}
	second, err := NewHandlerWithConfig(HandlerConfig{
		Store:          store.NewMemoryNodeStore(),
		Freshness:      DefaultFreshnessPolicy(),
		SigningKeyPath: keyPath,
	})
	if err != nil {
		t.Fatalf("build second handler: %v", err)
	}
	firstKey := readSigningKeyForTest(t, first)
	secondKey := readSigningKeyForTest(t, second)
	if firstKey != secondKey {
		t.Fatalf("persisted public key changed")
	}
}

func TestHeartbeatRecordsNode(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	credential := enrollTestNode(t, nodeStore, "node-1")

	body := protocol.HeartbeatRequest{
		NodeID:         "node-1",
		Hostname:       "worker-a",
		SidecarVersion: "dev",
		SentAt:         time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC),
		Runtimes: []protocol.RuntimeStatus{
			{
				Name:       "default",
				Type:       "hermes",
				State:      "running",
				Provider:   "openai",
				Model:      "gpt-5",
				ConfigHash: "sha256:abc",
			},
		},
		ConfigHash: "sha256:node",
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode heartbeat: %v", err)
	}

	handler := NewHandlerWithStore(nodeStore)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", &buf)
	req.Header.Set("Authorization", "Bearer "+credential)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var heartbeatResp protocol.HeartbeatResponse
	if err := json.NewDecoder(rec.Body).Decode(&heartbeatResp); err != nil {
		t.Fatalf("decode heartbeat response: %v", err)
	}
	if !heartbeatResp.Accepted {
		t.Fatalf("accepted = false, want true")
	}
	if heartbeatResp.Node.NodeID != "node-1" {
		t.Fatalf("nodeId = %q, want node-1", heartbeatResp.Node.NodeID)
	}
	if heartbeatResp.Node.State != protocol.NodeStateFresh {
		t.Fatalf("node state = %q, want fresh", heartbeatResp.Node.State)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/nodes", nil)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var nodes []protocol.NodeStatus
	if err := json.NewDecoder(rec.Body).Decode(&nodes); err != nil {
		t.Fatalf("decode nodes response: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes length = %d, want 1", len(nodes))
	}
	if nodes[0].NodeID != "node-1" {
		t.Fatalf("nodes[0].nodeId = %q, want node-1", nodes[0].NodeID)
	}
	if nodes[0].Runtimes[0].Type != "hermes" {
		t.Fatalf("runtime type = %q, want hermes", nodes[0].Runtimes[0].Type)
	}
}

func TestHeartbeatRequiresAuthorization(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(`{"nodeId":"node-1"}`))

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestHeartbeatRejectsWrongCredential(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	enrollTestNode(t, nodeStore, "node-1")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(`{"nodeId":"node-1"}`))
	req.Header.Set("Authorization", "Bearer wrong-credential")

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestHeartbeatRejectsCredentialNodeMismatch(t *testing.T) {
	nodeStore := store.NewMemoryNodeStore()
	nodeACredential := enrollTestNode(t, nodeStore, "node-a")
	enrollTestNode(t, nodeStore, "node-b")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(`{"nodeId":"node-b"}`))
	req.Header.Set("Authorization", "Bearer "+nodeACredential)

	NewHandlerWithStore(nodeStore).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestEnrollmentAPIsCreateTokenAndEnrollNode(t *testing.T) {
	handler := NewHandler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/enrollment-tokens", strings.NewReader(`{}`))

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusCreated)

	var tokenResp protocol.CreateEnrollmentTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode enrollment token response: %v", err)
	}
	if tokenResp.Token == "" {
		t.Fatalf("token is empty")
	}
	if tokenResp.ExpiresAt.IsZero() {
		t.Fatalf("expiresAt is zero")
	}

	var enrollBody bytes.Buffer
	if err := json.NewEncoder(&enrollBody).Encode(protocol.EnrollNodeRequest{
		Token:    tokenResp.Token,
		NodeID:   "node-enrolled",
		Hostname: "worker-enrolled",
	}); err != nil {
		t.Fatalf("encode enroll request: %v", err)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/enroll", &enrollBody)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var enrollResp protocol.EnrollNodeResponse
	if err := json.NewDecoder(rec.Body).Decode(&enrollResp); err != nil {
		t.Fatalf("decode enroll response: %v", err)
	}
	if enrollResp.NodeID != "node-enrolled" {
		t.Fatalf("nodeId = %q, want node-enrolled", enrollResp.NodeID)
	}
	if enrollResp.NodeCredential == "" {
		t.Fatalf("nodeCredential is empty")
	}
}

func TestHeartbeatRequiresNodeID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(`{"hostname":"worker-a"}`))

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusBadRequest)
}

func TestNodesApplyFreshnessPolicy(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	nodeStore := store.NewMemoryNodeStore()
	heartbeats := map[string]time.Time{
		"node-fresh":   now.Add(-time.Minute),
		"node-stale":   now.Add(-3 * time.Minute),
		"node-offline": now.Add(-11 * time.Minute),
	}
	for nodeID, observedAt := range heartbeats {
		_, err := nodeStore.RecordHeartbeat(context.Background(), protocol.HeartbeatRequest{
			NodeID:   nodeID,
			Hostname: nodeID,
		}, observedAt)
		if err != nil {
			t.Fatalf("record heartbeat for %s: %v", nodeID, err)
		}
	}

	handler, err := NewHandlerWithStoreAndFreshnessPolicy(nodeStore, FreshnessPolicy{
		StaleAfter:   2 * time.Minute,
		OfflineAfter: 10 * time.Minute,
		Now: func() time.Time {
			return now
		},
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var nodes []protocol.NodeStatus
	if err := json.NewDecoder(rec.Body).Decode(&nodes); err != nil {
		t.Fatalf("decode nodes response: %v", err)
	}

	got := make(map[string]protocol.NodeState)
	for _, node := range nodes {
		got[node.NodeID] = node.State
	}
	want := map[string]protocol.NodeState{
		"node-fresh":   protocol.NodeStateFresh,
		"node-stale":   protocol.NodeStateStale,
		"node-offline": protocol.NodeStateOffline,
	}
	for nodeID, wantState := range want {
		if got[nodeID] != wantState {
			t.Fatalf("node %s state = %q, want %q", nodeID, got[nodeID], wantState)
		}
	}
}

func TestNodesTreatZeroHeartbeatAsOffline(t *testing.T) {
	handler, err := NewHandlerWithStoreAndFreshnessPolicy(staticNodeStore{
		nodes: []protocol.NodeStatus{
			{
				NodeID:          "node-zero",
				State:           protocol.NodeStateFresh,
				LastHeartbeatAt: time.Time{},
			},
		},
	}, FreshnessPolicy{
		StaleAfter:   2 * time.Minute,
		OfflineAfter: 10 * time.Minute,
		Now: func() time.Time {
			return time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)

	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	var nodes []protocol.NodeStatus
	if err := json.NewDecoder(rec.Body).Decode(&nodes); err != nil {
		t.Fatalf("decode nodes response: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes length = %d, want 1", len(nodes))
	}
	if nodes[0].State != protocol.NodeStateOffline {
		t.Fatalf("node state = %q, want offline", nodes[0].State)
	}
}

func TestStatusEndpointsRejectNonGET(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusMethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, want %q", got, http.MethodGet)
	}
}

func TestHeartbeatRejectsNonPOST(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/heartbeat", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusMethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q, want %q", got, http.MethodPost)
	}
}

func TestNodesRejectsNonGET(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/nodes", nil)

	NewHandler().ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusMethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, want %q", got, http.MethodGet)
	}
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
}

func assertJSONStatus(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != want {
		t.Fatalf("status body = %q, want %q", body.Status, want)
	}
}

func readSigningKeyForTest(t *testing.T, handler http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/signing-key", nil)
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	var resp protocol.PublicSigningKeyResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode signing key response: %v", err)
	}
	return resp.PublicKey
}

func enrollTestNode(t *testing.T, nodeStore store.Store, nodeID string) string {
	t.Helper()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	tokenResp, err := nodeStore.CreateEnrollmentToken(context.Background(), now.Add(time.Hour), now)
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	enrollResp, err := nodeStore.EnrollNode(context.Background(), protocol.EnrollNodeRequest{
		Token:  tokenResp.Token,
		NodeID: nodeID,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("enroll test node %q: %v", nodeID, err)
	}
	if enrollResp.NodeCredential == "" {
		t.Fatalf("node credential is empty")
	}
	return enrollResp.NodeCredential
}

type staticNodeStore struct {
	nodes []protocol.NodeStatus
}

func (s staticNodeStore) RecordHeartbeat(context.Context, protocol.HeartbeatRequest, time.Time) (protocol.NodeStatus, error) {
	return protocol.NodeStatus{}, nil
}

func (s staticNodeStore) ListNodes(context.Context) ([]protocol.NodeStatus, error) {
	nodes := append([]protocol.NodeStatus(nil), s.nodes...)
	return nodes, nil
}

func (s staticNodeStore) NodeExists(context.Context, string) (bool, error) {
	return false, nil
}

func (s staticNodeStore) CreateEnrollmentToken(context.Context, time.Time, time.Time) (protocol.CreateEnrollmentTokenResponse, error) {
	return protocol.CreateEnrollmentTokenResponse{}, nil
}

func (s staticNodeStore) EnrollNode(context.Context, protocol.EnrollNodeRequest, time.Time) (protocol.EnrollNodeResponse, error) {
	return protocol.EnrollNodeResponse{}, nil
}

func (s staticNodeStore) VerifyNodeCredential(context.Context, string, string) (bool, error) {
	return false, nil
}

func (s staticNodeStore) CreateJob(context.Context, protocol.CreateJobRequest, string, time.Time) (protocol.Job, error) {
	return protocol.Job{}, nil
}

func (s staticNodeStore) GetJob(context.Context, string) (*protocol.Job, error) {
	return nil, nil
}

func (s staticNodeStore) ClaimNextJob(context.Context, string, time.Time) (*protocol.Job, error) {
	return nil, nil
}

func (s staticNodeStore) CompleteJob(context.Context, string, protocol.JobResultRequest, time.Time) error {
	return nil
}

func (s staticNodeStore) FailJob(context.Context, string, string, time.Time) error {
	return nil
}

func (s staticNodeStore) ListNodeJobs(context.Context, string) ([]protocol.Job, error) {
	return nil, nil
}

func (s staticNodeStore) AppendAuditEvent(context.Context, protocol.AuditEvent) (protocol.AuditEvent, error) {
	return protocol.AuditEvent{}, nil
}

func (s staticNodeStore) ListAuditEvents(context.Context, int) ([]protocol.AuditEvent, error) {
	return nil, nil
}

func (s staticNodeStore) GetDesiredConfig(context.Context) (protocol.DesiredConfig, error) {
	return protocol.DesiredConfig{}, nil
}

func (s staticNodeStore) SetDesiredConfig(context.Context, protocol.DesiredConfig, time.Time) error {
	return nil
}
