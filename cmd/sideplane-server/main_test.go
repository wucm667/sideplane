package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRejectsInvalidFreshnessDurations(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "offline before stale",
			args: []string{
				"--stale-after=10m",
				"--offline-after=2m",
			},
		},
		{
			name: "offline equals stale",
			args: []string{
				"--stale-after=10m",
				"--offline-after=10m",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			code := run(tt.args, &stdout, &stderr)

			if code == 0 {
				t.Fatalf("exit code = 0, want non-zero")
			}
			if !strings.Contains(stderr.String(), "offline-after must be greater than stale-after") {
				t.Fatalf("stderr = %q, want freshness validation error", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}
