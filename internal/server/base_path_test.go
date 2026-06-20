package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeBasePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "slash", in: "/", want: ""},
		{name: "adds leading slash", in: "sideplane", want: "/sideplane"},
		{name: "trims trailing slash", in: "/sideplane/", want: "/sideplane"},
		{name: "nested", in: "/ops/sideplane/", want: "/ops/sideplane"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeBasePath(tt.in)
			if err != nil {
				t.Fatalf("NormalizeBasePath(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeBasePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeBasePathRejectsNonPathValues(t *testing.T) {
	tests := []string{"/sideplane?x=1", "/sideplane#frag", "/side plane"}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, err := NormalizeBasePath(input); err == nil {
				t.Fatalf("NormalizeBasePath(%q) succeeded, want error", input)
			}
		})
	}
}

func TestBasePathHandlerServesAppUnderPrefix(t *testing.T) {
	app := newEmbeddedWebFixtureHandler(t)
	handler, err := NewBasePathHandler("/sideplane", app)
	if err != nil {
		t.Fatalf("NewBasePathHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sideplane/api/nodes", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("prefixed api status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("prefixed api Content-Type = %q, want application/json", ct)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/sideplane/nodes/worker-a", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("prefixed web status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Embedded SPA root") {
		t.Fatalf("prefixed web body = %q, want embedded SPA", body)
	}
}

func TestBasePathHandlerKeepsRootAndPrefixedProbes(t *testing.T) {
	app := newEmbeddedWebFixtureHandler(t)
	handler, err := NewBasePathHandler("/sideplane", app)
	if err != nil {
		t.Fatalf("NewBasePathHandler: %v", err)
	}

	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/sideplane/healthz", "/sideplane/readyz", "/sideplane/metrics"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s status = %d, want %d", path, rec.Code, http.StatusOK)
			}
		})
	}
}

func TestBasePathHandlerRejectsMisPrefixedAppRequests(t *testing.T) {
	app := newEmbeddedWebFixtureHandler(t)
	handler, err := NewBasePathHandler("/sideplane", app)
	if err != nil {
		t.Fatalf("NewBasePathHandler: %v", err)
	}

	for _, path := range []string{"/api/nodes", "/nodes/worker-a", "/sideplaneish/api/nodes"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s status = %d, want %d", path, rec.Code, http.StatusNotFound)
			}
		})
	}
}
