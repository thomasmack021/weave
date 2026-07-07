package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/thomasmack021/weave/internal/orchestrate"
	"github.com/thomasmack021/weave/internal/registry"
	"github.com/thomasmack021/weave/internal/validate"
	"github.com/thomasmack021/weave/web"
)

// Compile-time proof that the production orchestrator satisfies the
// consumer-side WorkspaceInitializer interface.
var _ WorkspaceInitializer = (*orchestrate.Orchestrator)(nil)

// stubInitializer is a recording WorkspaceInitializer test double, mirroring
// stubScaffolder: it captures the request and returns a canned Result/error.
type stubInitializer struct {
	calls  int
	gotReq orchestrate.InitRequest
	result orchestrate.Result
	err    error
}

func (s *stubInitializer) InitWorkspace(_ context.Context, req orchestrate.InitRequest) (orchestrate.Result, error) {
	s.calls++
	s.gotReq = req
	return s.result, s.err
}

func newWorkspaceServer(init WorkspaceInitializer) *Server {
	return New(web.Assets, registry.NewFakeRegistry(), &stubScaffolder{}, init)
}

// --- POST /api/workspace ---

// TestWorkspaceHappyPathReturns201AndIgnoresConfigFieldsInBody pins the Day 1
// wire contract: the client supplies only projectId and statePrefix;
// config-shaped fields in the body (repo URL, env, token) never reach the
// initializer.
func TestWorkspaceHappyPathReturns201AndIgnoresConfigFieldsInBody(t *testing.T) {
	init := &stubInitializer{result: orchestrate.Result{
		Changed: true,
		Branch:  "weave/init-dev",
		PRURL:   "https://bitbucket.example/pr/9",
	}}
	srv := newWorkspaceServer(init)

	rec := doJSON(t, srv, http.MethodPost, "/api/workspace", `{
		"projectId": "acme-prod-project",
		"statePrefix": "weave/dev",
		"repoUrl": "https://evil.example/hijack.git",
		"env": "prod",
		"token": "stolen"
	}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	decodeBody(t, rec, &body)
	if body["prUrl"] != init.result.PRURL || body["branch"] != init.result.Branch {
		t.Errorf("body = %v, want prUrl %q and branch %q", body, init.result.PRURL, init.result.Branch)
	}
	want := orchestrate.InitRequest{ProjectID: "acme-prod-project", StatePrefix: "weave/dev"}
	if init.gotReq != want {
		t.Errorf("initializer received %+v, want %+v (config-shaped body fields must be ignored)", init.gotReq, want)
	}
}

// TestWorkspaceMissingProjectIDReturns422 pins classification-only error
// handling: the server does not validate projectId itself; the orchestrator's
// pre-flight rejects it with validate.ErrMissingRequired, which must map to
// 422 like every other caller fault.
func TestWorkspaceMissingProjectIDReturns422(t *testing.T) {
	init := &stubInitializer{err: fmt.Errorf("orchestrate: validating init request: %w: projectId", validate.ErrMissingRequired)}
	srv := newWorkspaceServer(init)

	rec := doJSON(t, srv, http.MethodPost, "/api/workspace", `{"projectId": ""}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string][]string
	decodeBody(t, rec, &body)
	if len(body["errors"]) != 1 {
		t.Errorf("errors = %v, want exactly one entry", body["errors"])
	}
}

func TestWorkspaceMalformedJSONReturns400(t *testing.T) {
	init := &stubInitializer{}
	srv := newWorkspaceServer(init)

	rec := doJSON(t, srv, http.MethodPost, "/api/workspace", `{"projectId": `)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if init.calls != 0 {
		t.Errorf("initializer called %d times, want 0 for malformed JSON", init.calls)
	}
}

func TestWorkspaceIdempotentNoOpReturns200ChangedFalse(t *testing.T) {
	init := &stubInitializer{result: orchestrate.Result{Changed: false}}
	srv := newWorkspaceServer(init)

	rec := doJSON(t, srv, http.MethodPost, "/api/workspace", `{"projectId": "acme-prod-project"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]bool
	decodeBody(t, rec, &body)
	if changed, ok := body["changed"]; !ok || changed {
		t.Errorf("body = %v, want {\"changed\": false}", body)
	}
}

func TestWorkspacePRFailureAfterPushReturns502WithBranch(t *testing.T) {
	init := &stubInitializer{
		result: orchestrate.Result{Changed: true, Branch: "weave/init-dev"},
		err:    errors.New("orchestrate: opening pull request for pushed branch \"weave/init-dev\": bitbucket: rate limited"),
	}
	srv := newWorkspaceServer(init)

	rec := doJSON(t, srv, http.MethodPost, "/api/workspace", `{"projectId": "acme-prod-project"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	decodeBody(t, rec, &body)
	if body["branch"] != "weave/init-dev" {
		t.Errorf("body = %v, want the pushed branch reported for recovery", body)
	}
}

func TestWorkspaceInfrastructureFailureReturns500(t *testing.T) {
	init := &stubInitializer{err: errors.New("orchestrate: cloning: connection refused")}
	srv := newWorkspaceServer(init)

	rec := doJSON(t, srv, http.MethodPost, "/api/workspace", `{"projectId": "acme-prod-project"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceMethodNotAllowed(t *testing.T) {
	srv := newWorkspaceServer(&stubInitializer{})

	rec := doJSON(t, srv, http.MethodGet, "/api/workspace", "")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405; body: %s", rec.Code, rec.Body.String())
	}
}
