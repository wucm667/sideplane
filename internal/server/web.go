package server

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// isAPIPath reports whether a request path is reserved for the Sideplane API
// and must never be served as a static Web asset.
//
// The API owns the /api/ prefix plus the top-level health, readiness, and
// metrics endpoints. Everything else may be served from the Web UI directory.
func isAPIPath(path string) bool {
	if path == "/healthz" || path == "/readyz" || path == "/metrics" {
		return true
	}
	return strings.HasPrefix(path, "/api/") || path == "/api"
}

// NewWebHandler wraps api with a static-file handler that serves assets from
// webDir for non-API requests.
//
// Routing precedence:
//
//  1. API paths (/api/*, /healthz, /readyz, /metrics) always go to api.
//  2. Existing files under webDir are served verbatim.
//  3. Missing files fall back to webDir/index.html for SPA routing.
//
// When index.html is itself missing (for example because the UI has not been
// built), requests that would fall back to it return 404 instead of serving a
// directory listing.
func NewWebHandler(webDir string, api http.Handler) (http.Handler, error) {
	return NewWebHandlerWithBase(webDir, api, "")
}

// NewWebHandlerWithBase wraps api with a static-file handler and injects the
// configured base path into index.html for the browser client.
func NewWebHandlerWithBase(webDir string, api http.Handler, basePath string) (http.Handler, error) {
	normalizedBasePath, err := NormalizeBasePath(basePath)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(webDir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errNotADirectory{path: root}
	}

	fileServer := http.FileServer(http.Dir(root))

	webHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			api.ServeHTTP(w, r)
			return
		}

		serveWebAsset(w, r, root, fileServer, normalizedBasePath)
	})
	return securityHeaders(webHandler), nil
}

// NewEmbeddedWebHandler wraps api with a static-file handler that serves assets
// from an embedded filesystem for non-API requests.
func NewEmbeddedWebHandler(assets fs.FS, api http.Handler) http.Handler {
	return NewEmbeddedWebHandlerWithBase(assets, api, "")
}

// NewEmbeddedWebHandlerWithBase wraps api with an embedded static-file handler
// and injects the configured base path into index.html.
func NewEmbeddedWebHandlerWithBase(assets fs.FS, api http.Handler, basePath string) http.Handler {
	normalizedBasePath, err := NormalizeBasePath(basePath)
	if err != nil {
		normalizedBasePath = ""
	}
	fileServer := http.FileServer(http.FS(assets))

	webHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			api.ServeHTTP(w, r)
			return
		}

		serveEmbeddedWebAsset(w, r, assets, fileServer, normalizedBasePath)
	})
	return securityHeaders(webHandler)
}

// serveWebAsset serves a static asset from root, falling back to index.html
// for unknown SPA routes. Paths that look like static assets (have a file
// extension) return 404 when the file is missing, so broken asset references
// are not silently masked by the SPA shell.
func serveWebAsset(w http.ResponseWriter, r *http.Request, root string, fileServer http.Handler, basePath string) {
	requestPath := filepath.Clean("/" + r.URL.Path)
	cleanRoot := filepath.Clean(root)

	target := filepath.Join(cleanRoot, requestPath)
	// Guard against path traversal such as /../etc/passwd. filepath.Clean
	// collapses the request path, and the cleaned path must remain within
	// the web root.
	if !strings.HasPrefix(target+string(os.PathSeparator), cleanRoot+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}

	info, err := os.Stat(target)
	if err == nil && !info.IsDir() {
		fileServer.ServeHTTP(w, r)
		return
	}

	// If the request path has a file extension (e.g. /assets/missing.js,
	// /favicon.ico), it is a static asset reference rather than an SPA
	// route. Return 404 so broken asset links are visible.
	if hasFileExtension(requestPath) {
		http.NotFound(w, r)
		return
	}

	// Unknown route without a file extension: fall back to index.html for
	// SPA routing. If index.html does not exist, surface a 404 rather than
	// a directory listing.
	serveIndex(w, r, cleanRoot, basePath)
}

func serveEmbeddedWebAsset(w http.ResponseWriter, r *http.Request, assets fs.FS, fileServer http.Handler, basePath string) {
	requestPath := path.Clean("/" + r.URL.Path)
	assetPath := strings.TrimPrefix(requestPath, "/")
	if assetPath == "" {
		assetPath = "."
	}

	info, err := fs.Stat(assets, assetPath)
	if err == nil && !info.IsDir() {
		fileServer.ServeHTTP(w, r)
		return
	}

	if hasFileExtension(requestPath) {
		http.NotFound(w, r)
		return
	}

	serveEmbeddedIndex(w, r, assets, basePath)
}

// hasFileExtension reports whether the last path segment has a file
// extension. Go's filepath.Ext treats leading-dot names like ".hidden" as
// having extension ".hidden", so hidden files are handled as static assets.
func hasFileExtension(path string) bool {
	return filepath.Ext(filepath.Base(path)) != ""
}

func serveIndex(w http.ResponseWriter, r *http.Request, root string, basePath string) {
	index := filepath.Join(root, "index.html")
	info, err := os.Stat(index)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(index)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveIndexHTML(w, r, data, basePath)
}

func serveEmbeddedIndex(w http.ResponseWriter, r *http.Request, assets fs.FS, basePath string) {
	info, err := fs.Stat(assets, "index.html")
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveIndexHTML(w, r, data, basePath)
}

func serveIndexHTML(w http.ResponseWriter, r *http.Request, data []byte, basePath string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(injectWebBase(data, basePath))
	}
}

func injectWebBase(data []byte, basePath string) []byte {
	baseJSON, err := json.Marshal(basePath)
	if err != nil {
		baseJSON = []byte(`""`)
	}
	script := []byte(`<script>window.__SIDEPLANE_BASE__ = ` + string(baseJSON) + `;</script>`)
	headClose := []byte("</head>")
	if bytes.Contains(data, headClose) {
		return bytes.Replace(data, headClose, append(script, headClose...), 1)
	}
	return append(script, data...)
}

// errNotADirectory is returned when --web-dir points at something other than a
// directory.
type errNotADirectory struct {
	path string
}

func (e errNotADirectory) Error() string { return "web-dir is not a directory: " + e.path }
