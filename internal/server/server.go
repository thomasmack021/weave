// Package server hosts the Weave IDP HTTP API and serves the embedded frontend.
package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/thomasmack021/weave/internal/auth"
	"github.com/thomasmack021/weave/internal/orchestrate"
	"github.com/thomasmack021/weave/internal/registry"
)

// Scaffolder is the server's consumer-side view of the orchestration layer:
// one end-to-end Day 2 resource-addition run. *orchestrate.Orchestrator
// satisfies it in production (assembled in cmd/weaved); tests substitute a
// stub.
type Scaffolder interface {
	Run(ctx context.Context, req orchestrate.Request) (orchestrate.Result, error)
}

// WorkspaceInitializer is the server's consumer-side view of the Day 1
// workspace-initialization run backing /api/workspace.
// *orchestrate.Orchestrator satisfies it too; tests substitute a stub.
type WorkspaceInitializer interface {
	InitWorkspace(ctx context.Context, req orchestrate.InitRequest) (orchestrate.Result, error)
}

// Server is the HTTP entrypoint for the Weave IDP. It is constructed with its
// dependencies injected (no package-level globals), per the engagement rules.
type Server struct {
	// static is the embedded frontend asset tree (index.html at its root),
	// injected at construction time.
	static      fs.FS
	registry    registry.ModuleRegistry
	scaffolder  Scaffolder
	initializer WorkspaceInitializer
	// sessions is the optional identity/session layer. When nil (v1 / demo),
	// the server serves anonymously with no /api/session endpoint.
	sessions *auth.Service
	// useCases is the optional multi-tenant dispatcher. When nil, only the
	// single-tenant /api/scaffold + /api/workspace endpoints are served.
	useCases UseCaseService
}

// New constructs a Server with the embedded static asset filesystem, the
// module registry backing /api/catalog, the Scaffolder backing /api/scaffold,
// and the WorkspaceInitializer backing /api/workspace injected.
func New(static fs.FS, reg registry.ModuleRegistry, scaffolder Scaffolder, initializer WorkspaceInitializer) *Server {
	return &Server{static: static, registry: reg, scaffolder: scaffolder, initializer: initializer}
}

// WithSessions attaches the identity/session layer: it mounts /api/session and
// wraps every route with the principal-injecting middleware. Opt-in, so the v1
// single-tenant and demo paths keep running with no database. Returns the
// server for chaining.
func (s *Server) WithSessions(svc *auth.Service) *Server {
	s.sessions = svc
	return s
}

// WithUseCases attaches the multi-tenant dispatcher: it mounts the
// /api/usecases endpoints (list, scaffold, workspace, and admin management),
// each gated by the caller's RBAC role. Requires WithSessions to inject the
// principal. Opt-in; returns the server for chaining.
func (s *Server) WithUseCases(svc UseCaseService) *Server {
	s.useCases = svc
	return s
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
	mux.HandleFunc("/api/workspace", s.handleWorkspace)

	// Identity/session endpoints exist only when the session layer is attached.
	if s.sessions != nil {
		mux.HandleFunc("/api/session", s.sessions.HandleSession)
	}

	// Multi-tenant use-case endpoints (method + path-parameter patterns) exist
	// only when the dispatcher is attached.
	if s.useCases != nil {
		mux.HandleFunc("GET /api/usecases", s.handleListUseCases)
		mux.HandleFunc("POST /api/usecases", s.handleCreateUseCase)
		mux.HandleFunc("POST /api/usecases/{key}/scaffold", s.handleUseCaseScaffold)
		mux.HandleFunc("POST /api/usecases/{key}/workspace", s.handleUseCaseWorkspace)
		mux.HandleFunc("POST /api/usecases/{key}/members", s.handleAddMember)
		mux.HandleFunc("POST /api/usecases/{key}/groups", s.handleAddGroupGrant)
	}

	// Everything else is served from the embedded frontend asset tree. The
	// file server resolves "/" to index.html automatically.
	mux.Handle("/", http.FileServer(http.FS(s.static)))

	// When sessions are attached, wrap every route with the principal-injecting
	// middleware so handlers (and whoami) can read the caller from context.
	if s.sessions != nil {
		return s.sessions.Middleware(mux)
	}
	return mux
}

// handleHealth responds 200 OK with a small JSON status body.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// Static, fixed payload — encoding errors are not actionable here.
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
