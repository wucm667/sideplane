package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestJobJSONOmitUnsetTimestamps(t *testing.T) {
	job := Job{
		ID:        "job_pending",
		NodeID:    "node-1",
		Type:      JobTypeDeepProbe,
		Status:    JobStatusPending,
		CreatedAt: time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
	}

	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal pending job: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal pending job: %v", err)
	}
	if _, ok := got["claimedAt"]; ok {
		t.Fatalf("pending job JSON includes claimedAt: %s", payload)
	}
	if _, ok := got["finishedAt"]; ok {
		t.Fatalf("pending job JSON includes finishedAt: %s", payload)
	}
}

func TestJobJSONIncludesSetTimestamps(t *testing.T) {
	job := Job{
		ID:         "job_completed",
		NodeID:     "node-1",
		Type:       JobTypeDeepProbe,
		Status:     JobStatusCompleted,
		CreatedAt:  time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
		ClaimedAt:  time.Date(2026, 6, 16, 12, 1, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 6, 16, 12, 2, 0, 0, time.UTC),
	}

	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal completed job: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal completed job: %v", err)
	}
	if _, ok := got["claimedAt"]; !ok {
		t.Fatalf("completed job JSON omits claimedAt: %s", payload)
	}
	if _, ok := got["finishedAt"]; !ok {
		t.Fatalf("completed job JSON omits finishedAt: %s", payload)
	}
}

func TestRestartJobPayloadAndResultJSONRoundTrip(t *testing.T) {
	payload := RestartJobPayload{
		RuntimeType: "hermes",
		RuntimeName: "Hermes Agent",
		Profile:     "default",
		Reason:      "operator requested restart after config preview",
		DryRun:      true,
	}

	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal restart payload: %v", err)
	}

	var decodedPayload RestartJobPayload
	if err := json.Unmarshal(encodedPayload, &decodedPayload); err != nil {
		t.Fatalf("unmarshal restart payload: %v", err)
	}
	if decodedPayload != payload {
		t.Fatalf("restart payload round trip mismatch: %#v", decodedPayload)
	}

	result := RestartJobResult{
		Controller:   "fake-controller",
		HealthStatus: "healthy",
		Steps: []ConfigApplyStep{
			{Name: "plan", Status: "completed", Detail: "dry-run restart planned"},
			{Name: "restart", Status: "skipped", Detail: "dry-run"},
			{Name: "health_check", Status: "skipped", Detail: "dry-run"},
		},
	}

	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal restart result: %v", err)
	}

	var decodedResult RestartJobResult
	if err := json.Unmarshal(encodedResult, &decodedResult); err != nil {
		t.Fatalf("unmarshal restart result: %v", err)
	}
	if decodedResult.Controller != result.Controller || decodedResult.HealthStatus != result.HealthStatus {
		t.Fatalf("restart result scalar fields changed: %#v", decodedResult)
	}
	if len(decodedResult.Steps) != 3 || decodedResult.Steps[1].Status != "skipped" {
		t.Fatalf("restart result steps changed: %#v", decodedResult.Steps)
	}
}
