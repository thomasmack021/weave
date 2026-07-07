// Package server hosts the Weave IDP HTTP API and serves the embedded frontend.
package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/thomasmack021/weave/internal/orchestrate"
	"github.com/thomasmack021/weave/internal/registry"
)

// Scaffolder is the server's consumer-side view of the orchestration layer:
// one end-to-end scaffold run. *orchestrate.Orchestrator satisfies it in
// production (assembled in cmd/weaved); tests substitute a stub.
type Scaffolder interface {
	Run(ctx context.Context, req orchestrate.Request) (orchestrate.Result, error)
}

// Server is the HTTP entrypoint for the Weave IDP. It is constructed with its
// dependencies injected (no package-level globals), per the engagement rules.
type Server struct {
	// static is the embedded frontend asset tree (index.html at its root),
	// injected at construction time.
	static     fs.FS
	registry   registry.ModuleRegistry
	scaffolder Scaffolder
}

// New constructs a Server with the embedded static asset filesystem, the
// module registry backing /api/catalog, and the Scaffolder backing
// /api/scaffold injected.
func New(static fs.FS, reg registry.ModuleRegistry, scaffolder Scaffolder) *Server {
	return &Server{static: static, registry: reg, scaffolder: scaffolder}
}

// Handler returns the root HTTP handler for the server. It wires the liveness
// probe and serves the injected embedded frontend at the site root.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Liveness probe: proves the server process is up and routing.
	mux.HandleFunc("/health", s.handleHealth)

	// JSON API. Method checks live in the handlers (go.mod targets 1.21, so
	// method-qualified mux patterns are not available).
	mux.HandleFunc("/api/catalog", s.handleCatalog)
	mux.HandleFunc("/api/scaffold", s.handleScaffold)

	// Everything else is served from the embedded frontend asset tree. The
	// file server resolves "/" to index.html automatically.
	mux.Handle("/", http.FileServer(http.FS(s.static)))

	return mux
}

// handleHealth responds 200 OK with a small JSON status body.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// Static, fixed payload — encoding errors are not actionable here.
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
