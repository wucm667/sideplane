package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRolloutJSONRoundTrip(t *testing.T) {
	createdAt := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	rollout := Rollout{
		ID:    "rollout_123",
		State: RolloutStateRunning,
		Spec: RolloutSpec{
			Selector: map[string]string{
				"role": "canary",
				"zone": "lab",
			},
			RuntimeType:   "hermes",
			Profile:       "default",
			Target:        ProviderModelConfig{Provider: "openai", Model: "gpt-5"},
			BatchSize:             1,
			Live:                  true,
			HealthTimeout:         5 * time.Minute,
			AutoRollbackOnFailure: true,
		},
		Batches: []RolloutBatch{{
			Index:   0,
			NodeIDs: []string{"node-a"},
			State:   RolloutBatchStateRunning,
			Nodes: map[string]RolloutNodeProgress{
				"node-a": {
					NodeID:        "node-a",
					JobID:         "job_apply",
					State:         RolloutNodeStateFailed,
					StartedAt:     createdAt.Add(time.Minute),
					RollbackJobID: "job_rollback",
					RolledBack:    true,
				},
			},
		}},
		CreatedAt: createdAt,
		UpdatedAt: createdAt.Add(2 * time.Minute),
	}

	payload, err := json.Marshal(rollout)
	if err != nil {
		t.Fatalf("marshal rollout: %v", err)
	}
	var decoded Rollout
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal rollout: %v", err)
	}
	if decoded.ID != rollout.ID || decoded.State != RolloutStateRunning {
		t.Fatalf("decoded rollout = %#v, want ID/state preserved", decoded)
	}
	if decoded.Spec.Target.Provider != "openai" || decoded.Spec.HealthTimeout != 5*time.Minute {
		t.Fatalf("decoded spec = %#v, want target and timeout preserved", decoded.Spec)
	}
	if !decoded.Spec.AutoRollbackOnFailure {
		t.Fatalf("decoded spec = %#v, want autoRollbackOnFailure preserved", decoded.Spec)
	}
	node := decoded.Batches[0].Nodes["node-a"]
	if node.JobID != "job_apply" {
		t.Fatalf("decoded node progress = %#v, want job_apply", node)
	}
	if node.RollbackJobID != "job_rollback" || !node.RolledBack {
		t.Fatalf("decoded node progress = %#v, want rollback fields preserved", node)
	}
}

func TestRolloutRequestResponseJSONShapes(t *testing.T) {
	create := CreateRolloutRequest{Spec: RolloutSpec{
		NodeIDs:     []string{"node-a", "node-b"},
		RuntimeType: "openclaw",
		Target:      ProviderModelConfig{Provider: "anthropic", Model: "claude-sonnet-4"},
		BatchSize:   2,
		Live:        true,
	}}
	payload, err := json.Marshal(create)
	if err != nil {
		t.Fatalf("marshal create request: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(payload, &asMap); err != nil {
		t.Fatalf("unmarshal create request as map: %v", err)
	}
	if _, ok := asMap["spec"]; !ok {
		t.Fatalf("create request omits spec: %s", payload)
	}

	actionPayload, err := json.Marshal(RolloutActionRequest{Action: RolloutActionAbort})
	if err != nil {
		t.Fatalf("marshal action request: %v", err)
	}
	var action RolloutActionRequest
	if err := json.Unmarshal(actionPayload, &action); err != nil {
		t.Fatalf("unmarshal action request: %v", err)
	}
	if action.Action != RolloutActionAbort {
		t.Fatalf("action = %q, want abort", action.Action)
	}

	listPayload, err := json.Marshal(ListRolloutsResponse{
		Rollouts: []Rollout{{ID: "rollout_1", State: RolloutStatePaused}},
		Total:    1,
		Limit:    50,
		Offset:   0,
	})
	if err != nil {
		t.Fatalf("marshal list response: %v", err)
	}
	var list ListRolloutsResponse
	if err := json.Unmarshal(listPayload, &list); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if list.Total != 1 || list.Rollouts[0].State != RolloutStatePaused {
		t.Fatalf("list response = %#v, want one paused rollout", list)
	}
}
