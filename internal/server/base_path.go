package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// NormalizeBasePath converts a configured base path into the canonical form
// used by the HTTP router: empty, or a leading slash with no trailing slash.
func NormalizeBasePath(basePath string) (string, error) {
	basePath = strings.TrimSpace(basePath)
	if basePath == "" || basePath == "/" {
		return "", nil
	}
	if strings.Contains(basePath, "?") || strings.Contains(basePath, "#") {
		return "", fmt.Errorf("base path must be a URL path, got %q", basePath)
	}
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	basePath = strings.TrimRight(basePath, "/")
	if basePath == "" {
		return "", nil
	}
	escaped := (&url.URL{Path: basePath}).EscapedPath()
	if escaped != basePath {
		return "", fmt.Errorf("base path must not require escaping: %q", basePath)
	}
	return basePath, nil
}

// NewBasePathHandler serves app under basePath while keeping root probes
// reachable for health checks and metrics scrapers.
func NewBasePathHandler(basePath string, app http.Handler) (http.Handler, error) {
	normalized, err := NormalizeBasePath(basePath)
	if err != nil {
		return nil, err
	}
	if normalized == "" {
		return app, nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isRootProbePath(r.URL.Path) {
			app.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == normalized {
			app.ServeHTTP(w, withRequestPath(r, "/"))
			return
		}
		if strings.HasPrefix(r.URL.Path, normalized+"/") {
			app.ServeHTTP(w, withRequestPath(r, strings.TrimPrefix(r.URL.Path, normalized)))
			return
		}
		http.NotFound(w, r)
	}), nil
}

func isRootProbePath(requestPath string) bool {
	return requestPath == "/healthz" || requestPath == "/readyz" || requestPath == "/metrics"
}

func withRequestPath(r *http.Request, requestPath string) *http.Request {
	cloned := r.Clone(r.Context())
	urlCopy := *r.URL
	urlCopy.Path = requestPath
	urlCopy.RawPath = ""
	cloned.URL = &urlCopy
	return cloned
}
