package server

import (
	"net/http"
	"os"
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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			api.ServeHTTP(w, r)
			return
		}

		serveWebAsset(w, r, root, fileServer)
	}), nil
}

// serveWebAsset serves a static asset from root, falling back to index.html
// for unknown paths so client-side SPA routing works.
func serveWebAsset(w http.ResponseWriter, r *http.Request, root string, fileServer http.Handler) {
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

	// Unknown file or directory request: fall back to index.html for SPA
	// routing. If index.html does not exist, surface a 404 rather than a
	// directory listing.
	serveIndex(w, r, cleanRoot)
}

func serveIndex(w http.ResponseWriter, r *http.Request, root string) {
	index := filepath.Join(root, "index.html")
	info, err := os.Stat(index)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, index)
}

// errNotADirectory is returned when --web-dir points at something other than a
// directory.
type errNotADirectory struct {
	path string
}

func (e errNotADirectory) Error() string { return "web-dir is not a directory: " + e.path }
