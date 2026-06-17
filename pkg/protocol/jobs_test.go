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
