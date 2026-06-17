package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWebFixture creates a temporary web directory containing index.html and
// an assets subdirectory with one file. It returns the directory path.
func writeWebFixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "index.html"), "<!doctype html><html><body>SPA root</body></html>")
	mustWriteFile(t, filepath.Join(dir, "assets", "app.js"), "// app js")
	mustWriteFile(t, filepath.Join(dir, "favicon.ico"), "ico-bytes")
	return dir
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func newWebFixtureHandler(t *testing.T) http.Handler {
	t.Helper()
	dir := writeWebFixture(t)
	handler, err := NewWebHandler(dir, NewHandler())
	if err != nil {
		t.Fatalf("NewWebHandler: %v", err)
	}
	return handler
}

func TestWebAPIRoutesTakePrecedence(t *testing.T) {
	handler := newWebFixtureHandler(t)

	apiPaths := []string{
		"/healthz",
		"/readyz",
		"/metrics",
		"/api/nodes",
		"/api/heartbeat",
		"/api/enrollment-tokens",
		"/api",
	}
	for _, path := range apiPaths {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)

		handler.ServeHTTP(rec, req)

		body := rec.Body.String()
		if strings.Contains(body, "SPA root") {
			t.Fatalf("path %s was served by the Web UI, want the API; body: %s", path, body)
		}
	}

	// /api/nodes should return JSON from the API rather than index.html.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	handler.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
}

func TestWebRootServesIndexHTML(t *testing.T) {
	handler := newWebFixtureHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); !strings.Contains(got, "SPA root") {
		t.Fatalf("body = %q, want index.html content", got)
	}
}

func TestWebSPARouteFallsBackToIndexHTML(t *testing.T) {
	handler := newWebFixtureHandler(t)

	for _, path := range []string{"/nodes", "/nodes/worker-a", "/fleet/worker-a/runtime"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("path %s status = %d, want %d", path, rec.Code, http.StatusOK)
		}
		if got := rec.Body.String(); !strings.Contains(got, "SPA root") {
			t.Fatalf("path %s body = %q, want index.html content", path, got)
		}
	}
}

func TestWebServesRealAsset(t *testing.T) {
	handler := newWebFixtureHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "// app js" {
		t.Fatalf("body = %q, want app.js content", got)
	}
}

func TestWebReturns404WhenIndexMissing(t *testing.T) {
	dir := t.TempDir()
	// A directory with assets but no index.html.
	mustWriteFile(t, filepath.Join(dir, "robots.txt"), "user-agent: *")

	handler, err := NewWebHandler(dir, NewHandler())
	if err != nil {
		t.Fatalf("NewWebHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/unknown-route", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestWebHandlerRejectsMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := NewWebHandler(missing, NewHandler()); err == nil {
		t.Fatalf("NewWebHandler with missing dir: want error, got nil")
	}
}

func TestWebHandlerRejectsFileAsWebDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	mustWriteFile(t, file, "contents")

	if _, err := NewWebHandler(file, NewHandler()); err == nil {
		t.Fatalf("NewWebHandler with file: want error, got nil")
	}
}

func TestWebHandlerEmptyWebDirKeepsAPIOnly(t *testing.T) {
	// Sanity: an empty web directory still serves the API and falls back to
	// 404 for unknown paths because index.html is absent.
	dir := t.TempDir()

	handler, err := NewWebHandler(dir, NewHandler())
	if err != nil {
		t.Fatalf("NewWebHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/some-route", nil)
	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusNotFound)
}

func TestWebRootRequestWithHEADReturnsIndex(t *testing.T) {
	handler := newWebFixtureHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if _, err := io.ReadAll(rec.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
}
