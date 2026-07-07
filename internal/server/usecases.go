package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/thomasmack021/weave/internal/auth"
	"github.com/thomasmack021/weave/internal/orchestrate"
	"github.com/thomasmack021/weave/internal/store"
	"github.com/thomasmack021/weave/internal/usecase"
)

// UseCaseService is the server's consumer-side view of the multi-tenant
// dispatcher. *usecase.Service satisfies it; tests substitute a stub.
type UseCaseService interface {
	List(ctx context.Context, p store.Principal) ([]store.UseCase, error)
	Scaffold(ctx context.Context, p store.Principal, key string, req orchestrate.Request) (orchestrate.Result, error)
	InitWorkspace(ctx context.Context, p store.Principal, key string, req orchestrate.InitRequest) (orchestrate.Result, error)
	CreateUseCase(ctx context.Context, p store.Principal, uc store.UseCase) (store.UseCase, error)
	AddMember(ctx context.Context, p store.Principal, key, subject string, role store.Role) error
	AddGroupGrant(ctx context.Context, p store.Principal, key, group string, role store.Role) error
}

// The production dispatcher must satisfy the consumer-side interface.
var _ UseCaseService = (*usecase.Service)(nil)

// principal reads the authenticated principal the session middleware injected,
// writing 401 and returning ok=false when the request is anonymous.
func (s *Server) principal(w http.ResponseWriter, r *http.Request) (store.Principal, bool) {
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
	}
	return p, ok
}

// useCaseDTO is the tenant-safe projection returned to the client: enough to
// choose a use case, never its repo URL, credentials, or credential ref.
type useCaseDTO struct {
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
	Env         string `json:"env"`
}

// handleListUseCases backs GET /api/usecases: the use cases the caller may act
// on (already RBAC-filtered by the store).
func (s *Server) handleListUseCases(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	list, err := s.useCases.List(r.Context(), p)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]useCaseDTO, 0, len(list))
	for _, uc := range list {
		out = append(out, useCaseDTO{Key: uc.Key, DisplayName: uc.DisplayName, Env: uc.Env})
	}
	writeJSON(w, http.StatusOK, map[string]any{"useCases": out})
}

// handleUseCaseScaffold backs POST /api/usecases/{key}/scaffold.
func (s *Server) handleUseCaseScaffold(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	var req scaffoldRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed JSON body: " + err.Error()})
		return
	}
	result, err := s.useCases.Scaffold(r.Context(), p, r.PathValue("key"), orchestrate.Request{
		ModuleType:   req.ModuleType,
		InstanceName: req.InstanceName,
		Inputs:       req.Inputs,
	})
	s.writeTenantResult(w, result, err)
}

// handleUseCaseWorkspace backs POST /api/usecases/{key}/workspace.
func (s *Server) handleUseCaseWorkspace(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	var req workspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed JSON body: " + err.Error()})
		return
	}
	result, err := s.useCases.InitWorkspace(r.Context(), p, r.PathValue("key"), orchestrate.InitRequest{
		ProjectID:   req.ProjectID,
		StatePrefix: req.StatePrefix,
	})
	s.writeTenantResult(w, result, err)
}

// createUseCaseRequest is the admin payload for onboarding a use case.
type createUseCaseRequest struct {
	Key           string `json:"key"`
	DisplayName   string `json:"displayName"`
	RepoURL       string `json:"repoUrl"`
	RepoSlug      string `json:"repoSlug"`
	PRProvider    string `json:"prProvider"`
	BaseBranch    string `json:"baseBranch"`
	Env           string `json:"env"`
	CredentialRef string `json:"credentialRef"`
}

// handleCreateUseCase backs POST /api/usecases (admin).
func (s *Server) handleCreateUseCase(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	var req createUseCaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed JSON body: " + err.Error()})
		return
	}
	uc, err := s.useCases.CreateUseCase(r.Context(), p, store.UseCase{
		Key:           req.Key,
		DisplayName:   req.DisplayName,
		RepoURL:       req.RepoURL,
		RepoSlug:      req.RepoSlug,
		PRProvider:    req.PRProvider,
		BaseBranch:    req.BaseBranch,
		Env:           req.Env,
		CredentialRef: req.CredentialRef,
	})
	if err != nil {
		s.writeTenantError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, useCaseDTO{Key: uc.Key, DisplayName: uc.DisplayName, Env: uc.Env})
}

// grantRequest is the admin payload for adding a membership or group grant.
type grantRequest struct {
	Subject string `json:"subject"` // for member grants
	Group   string `json:"group"`   // for group grants
	Role    string `json:"role"`
}

// handleAddMember backs POST /api/usecases/{key}/members (admin).
func (s *Server) handleAddMember(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	var req grantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed JSON body: " + err.Error()})
		return
	}
	err := s.useCases.AddMember(r.Context(), p, r.PathValue("key"), req.Subject, store.Role(req.Role))
	s.writeGrantResult(w, err)
}

// handleAddGroupGrant backs POST /api/usecases/{key}/groups (admin).
func (s *Server) handleAddGroupGrant(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	var req grantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed JSON body: " + err.Error()})
		return
	}
	err := s.useCases.AddGroupGrant(r.Context(), p, r.PathValue("key"), req.Group, store.Role(req.Role))
	s.writeGrantResult(w, err)
}

// writeTenantResult maps a scaffold/workspace outcome (RBAC + orchestrate) to a
// status: 201/200 on success, 404/403 for tenancy faults, then the same
// classification as the single-tenant endpoints (422 caller faults, 502
// pushed-but-no-PR, 500 otherwise).
func (s *Server) writeTenantResult(w http.ResponseWriter, result orchestrate.Result, err error) {
	switch {
	case err == nil:
		if !result.Changed {
			writeJSON(w, http.StatusOK, map[string]bool{"changed": false})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"prUrl": result.PRURL, "branch": result.Branch})
	case errors.Is(err, usecase.ErrUseCaseNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, usecase.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case isRequestError(err):
		writeJSON(w, http.StatusUnprocessableEntity, map[string][]string{"errors": errorMessages(err)})
	case result.Branch != "":
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error(), "branch": result.Branch})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// writeTenantError maps an admin-op error (create) to a status.
func (s *Server) writeTenantError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, usecase.ErrUseCaseNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, usecase.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// writeGrantResult maps a membership/group-grant outcome: 204 on success, else
// the tenancy classification (404/403/500).
func (s *Server) writeGrantResult(w http.ResponseWriter, err error) {
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.writeTenantError(w, err)
}
