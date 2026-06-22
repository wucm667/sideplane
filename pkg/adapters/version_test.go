package adapters

import "testing"

func TestImageTagVersion(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "tagged image", ref: "nousresearch/hermes-agent:v2026.4.30", want: "v2026.4.30"},
		{name: "latest tag", ref: "openclaw:latest", want: "latest"},
		{name: "registry port without tag", ref: "localhost:5000/openclaw", want: "localhost:5000/openclaw"},
		{name: "tag and digest", ref: "ghcr.io/acme/openclaw:v1@sha256:abc", want: "v1"},
		{name: "digest only", ref: "ghcr.io/acme/openclaw@sha256:abc", want: "ghcr.io/acme/openclaw@sha256:abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ImageTagVersion(tt.ref); got != tt.want {
				t.Fatalf("ImageTagVersion(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestParseVersionCommandSplitsWithoutShell(t *testing.T) {
	name, args, err := ParseVersionCommand(" /usr/local/bin/hermes --version ")
	if err != nil {
		t.Fatalf("ParseVersionCommand error = %v", err)
	}
	if name != "/usr/local/bin/hermes" {
		t.Fatalf("name = %q, want hermes path", name)
	}
	if len(args) != 1 || args[0] != "--version" {
		t.Fatalf("args = %#v, want --version", args)
	}
}

func TestParseVersionCommandRejectsShell(t *testing.T) {
	_, _, err := ParseVersionCommand("sh -c hermes --version")
	if err == nil {
		t.Fatal("ParseVersionCommand error = nil, want shell rejection")
	}
}
