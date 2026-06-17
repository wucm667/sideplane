package sidecar

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReadStateLoadsNonDefaultPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom-sidecar-state.json")
	enrolledAt := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	want := SidecarState{
		ServerURL:      "http://localhost:8080",
		NodeID:         "node-1",
		NodeCredential: "test-credential",
		EnrolledAt:     enrolledAt,
	}

	if err := WriteState(path, want); err != nil {
		t.Fatalf("write state: %v", err)
	}

	got, err := ReadState(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if got.ServerURL != want.ServerURL {
		t.Fatalf("serverUrl = %q, want %q", got.ServerURL, want.ServerURL)
	}
	if got.NodeID != want.NodeID {
		t.Fatalf("nodeId = %q, want %q", got.NodeID, want.NodeID)
	}
	if got.NodeCredential != want.NodeCredential {
		t.Fatalf("nodeCredential = %q, want %q", got.NodeCredential, want.NodeCredential)
	}
	if !got.EnrolledAt.Equal(want.EnrolledAt) {
		t.Fatalf("enrolledAt = %s, want %s", got.EnrolledAt, want.EnrolledAt)
	}
}
