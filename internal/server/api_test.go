package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/thomasmack021/weave/internal/orchestrate"
	"github.com/thomasmack021/weave/internal/registry"
	"github.com/thomasmack021/weave/internal/validate"
	"github.com/thomasmack021/weave/web"
)

// Compile-time proof that the production orchestrator satisfies the
// consumer-side Scaffolder interface (nothing else enforces this until
// cmd/weaved assembles the two in step 5).
var _ Scaffolder = (*orchestrate.Orchestrator)(nil)

// stubScaffolder is a recording Scaffolder test double: it captures the
// request it was invoked with and returns a canned Result/error, so tests can
// assert both the wire mapping and exactly what crossed the interface.
type stubScaffolder struct {
	calls  int
	gotReq orchestrate.Request
	result orchestrate.Result
	err    error
}

func (s *stubScaffolder) Run(_ context.Context, req orchestrate.Request) (orchestrate.Result, error) {
	s.calls++
	s.gotReq = req
	return s.result, s.err
}

// failingRegistry is a ModuleRegistry whose every call fails, for exercising
// the catalog 500 path.
type failingRegistry struct{ err error }

func (f *failingRegistry) Resolve(context.Context, string) (*registry.ModuleSpec, error) {
	return nil, f.err
}
func (f *failingRegistry) List(context.Context) ([]registry.ModuleSpec, error) {
	return nil, f.err
}

// postgresSpec is the shared catalog fixture. Its Source deliberately carries
// a recognizable internal URL so tests can prove it never leaks to clients.
func postgresSpec() registry.ModuleSpec {
	return registry.ModuleSpec{
		Name:        "postgres",
		DisplayName: "PostgreSQL",
		Description: "Managed PostgreSQL database",
		Category:    "database",
		Source:      "git::https://bitbucket.org/acme/iac-modules//postgres",
		Version:     "1.2.0",
		Stability:   "stable",
		Inputs: []registry.InputSpec{
			{Name: "name", Type: "string", Required: true, Description: "Instance name"},
			{Name: "storage_gb", Type: "number", Required: false, Description: "Disk size in GB"},
		},
	}
}

func newAPIServer(reg registry.ModuleRegistry, sc Scaffolder) *Server {
	return New(web.Assets, reg, sc, &stubInitializer{})
}

func doJSON(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.NewDecoder(rec.Result().Body).Decode(into); err != nil {
		t.Fatalf("decoding response body: %v\nbody: %s", err, rec.Body.String())
	}
}

// --- GET /api/catalog ---

func TestCatalogReturnsModuleDTOsWithoutSource(t *testing.T) {
	srv := newAPIServer(registry.NewFakeRegistry(postgresSpec()), &stubScaffolder{})

	rec := doJSON(t, srv, http.MethodGet, "/api/catalog", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/catalog: got status %d, want %d\nbody: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("GET /api/catalog: got Content-Type %q, want application/json", ct)
	}

	var got []struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		Description string `json:"description"`
		Version     string `json:"version"`
		Inputs      []struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			Required    bool   `json:"required"`
			Description string `json:"description"`
		} `json:"inputs"`
	}
	decodeBody(t, rec, &got)

	if len(got) != 1 {
		t.Fatalf("catalog: got %d modules, want 1", len(got))
	}
	m := got[0]
	if m.Name != "postgres" || m.DisplayName != "PostgreSQL" || m.Version != "1.2.0" {
		t.Fatalf("catalog module DTO mismatch: %+v", m)
	}
	if len(m.Inputs) != 2 {
		t.Fatalf("catalog: got %d inputs, want 2: %+v", len(m.Inputs), m.Inputs)
	}
	if m.Inputs[0].Name != "name" || !m.Inputs[0].Required || m.Inputs[0].Type != "string" {
		t.Fatalf("catalog: first input DTO mismatch: %+v", m.Inputs[0])
	}

	// The DTO boundary must not leak the module's git Source to clients.
	if strings.Contains(rec.Body.String(), "iac-modules") {
		t.Fatalf("catalog response leaked module Source:\n%s", rec.Body.String())
	}
}

// choiceSpec is a catalog fixture with a `type: choice` input. Its ExpandsTo
// values carry recognizable markers so the test can prove the expansion map
// never reaches the browser — option expansion is the platform team's
// implementation detail, applied server-side by validate.Inputs.
func choiceSpec() registry.ModuleSpec {
	return registry.ModuleSpec{
		Name:        "cloud-run",
		DisplayName: "Cloud Run Service",
		Description: "Deploy a container",
		Version:     "2.0.0",
		Inputs: []registry.InputSpec{
			{Name: "service_name", Type: "string", Required: true, Description: "Service name"},
			{Name: "cpu", Type: "number", TfvarsKey: "cpu", ModuleArg: "cpu"},
			{Name: "memory", Type: "string", TfvarsKey: "memory", ModuleArg: "memory"},
			{
				Name:        "size",
				Type:        "choice",
				Required:    true,
				Description: "How big should this service be?",
				Options: []registry.OptionSpec{
					{
						Value:       "small",
						Label:       "Small — for prototypes",
						Description: "1 vCPU, 512Mi memory",
						ExpandsTo:   map[string]any{"cpu": 1, "memory": "SECRET-EXPANSION-512Mi"},
					},
					{
						Value:     "large",
						Label:     "Large — production workloads",
						ExpandsTo: map[string]any{"cpu": 4, "memory": "SECRET-EXPANSION-2Gi"},
					},
				},
			},
		},
	}
}

// TestCatalogExposesChoiceOptionsWithoutExpansion drives Phase 2 step 4: the
// catalog DTO must carry each choice input's options (value, label,
// description — what a wizard needs to render business-language radio
// buttons) while the expandsTo map, like the module Source, never leaves the
// server.
func TestCatalogExposesChoiceOptionsWithoutExpansion(t *testing.T) {
	srv := newAPIServer(registry.NewFakeRegistry(choiceSpec()), &stubScaffolder{})

	rec := doJSON(t, srv, http.MethodGet, "/api/catalog", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/catalog: got status %d, want %d\nbody: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got []struct {
		Inputs []struct {
			Name            string `json:"name"`
			Type            string `json:"type"`
			ManagedByChoice bool   `json:"managedByChoice"`
			Options         []struct {
				Value       string `json:"value"`
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"inputs"`
	}
	decodeBody(t, rec, &got)

	if len(got) != 1 {
		t.Fatalf("catalog: got %d modules, want 1", len(got))
	}

	// Inputs set by some option's expansion are flagged so a wizard knows not
	// to render them as free-form fields (a direct value would collide with
	// the expansion as ErrChoiceConflict). The flag reveals only that the
	// platform manages the input — never which choice sets what.
	wantManaged := map[string]bool{"service_name": false, "cpu": true, "memory": true, "size": false}
	for _, in := range got[0].Inputs {
		if want, ok := wantManaged[in.Name]; ok && in.ManagedByChoice != want {
			t.Errorf("input %q managedByChoice = %v, want %v", in.Name, in.ManagedByChoice, want)
		}
	}
	var choice *struct {
		Value       string `json:"value"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}
	for _, in := range got[0].Inputs {
		if in.Name == "size" {
			if in.Type != "choice" {
				t.Fatalf("size input type = %q, want %q", in.Type, "choice")
			}
			if len(in.Options) != 2 {
				t.Fatalf("size input: got %d options, want 2", len(in.Options))
			}
			choice = &in.Options[0]
		}
	}
	if choice == nil {
		t.Fatalf("catalog DTO missing the size choice input: %s", rec.Body.String())
	}
	if choice.Value != "small" || choice.Label != "Small — for prototypes" || choice.Description != "1 vCPU, 512Mi memory" {
		t.Fatalf("first option DTO mismatch: %+v", *choice)
	}

	// The expansion map is server-side only: neither the JSON key nor any
	// expansion value may appear in the response.
	body := rec.Body.String()
	for _, leak := range []string{"expandsTo", "ExpandsTo", "SECRET-EXPANSION"} {
		if strings.Contains(body, leak) {
			t.Fatalf("catalog response leaked option expansion (%q):\n%s", leak, body)
		}
	}
}

func TestCatalogRegistryFailureReturns500(t *testing.T) {
	srv := newAPIServer(&failingRegistry{err: errors.New("spec.yaml unreadable")}, &stubScaffolder{})

	rec := doJSON(t, srv, http.MethodGet, "/api/catalog", "")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET /api/catalog with failing registry: got status %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// --- POST /api/scaffold ---

func TestScaffoldHappyPathReturns201AndIgnoresConfigFieldsInBody(t *testing.T) {
	stub := &stubScaffolder{result: orchestrate.Result{
		Changed: true,
		Branch:  "weave/add-orders-db",
		PRURL:   "https://bitbucket.org/acme/infra/pull-requests/7",
	}}
	srv := newAPIServer(registry.NewFakeRegistry(postgresSpec()), stub)

	// The body smuggles config-shaped fields; the contract is that repo URL,
	// base branch, env, and token come from server config only, so nothing
	// beyond moduleType/instanceName/inputs may reach the Scaffolder.
	body := `{
		"moduleType": "postgres",
		"instanceName": "orders-db",
		"inputs": {"name": "orders-db"},
		"repoUrl": "https://attacker.example/repo.git",
		"baseBranch": "prod",
		"token": "stolen",
		"env": "prod"
	}`
	rec := doJSON(t, srv, http.MethodPost, "/api/scaffold", body)

	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/scaffold: got status %d, want %d\nbody: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var got struct {
		PRURL  string `json:"prUrl"`
		Branch string `json:"branch"`
	}
	decodeBody(t, rec, &got)
	if got.PRURL != stub.result.PRURL || got.Branch != stub.result.Branch {
		t.Fatalf("scaffold response mismatch: got %+v, want prUrl=%q branch=%q", got, stub.result.PRURL, stub.result.Branch)
	}

	if stub.calls != 1 {
		t.Fatalf("scaffolder called %d times, want 1", stub.calls)
	}
	want := orchestrate.Request{
		ModuleType:   "postgres",
		InstanceName: "orders-db",
		Inputs:       map[string]string{"name": "orders-db"},
	}
	if !reflect.DeepEqual(stub.gotReq, want) {
		t.Fatalf("scaffolder received %+v, want %+v (request-body config fields must be ignored)", stub.gotReq, want)
	}
}

func TestScaffoldValidationFailureReturns422WithOneEntryPerError(t *testing.T) {
	// Reproduce exactly the error shape the orchestrator emits: a wrapping
	// fmt.Errorf around the errors.Join accumulated by validate.Inputs.
	joined := errors.Join(
		fmt.Errorf("%q: %w", "size", validate.ErrUnknownInput),
		fmt.Errorf("%q: %w", "name", validate.ErrMissingRequired),
	)
	stub := &stubScaffolder{err: fmt.Errorf("orchestrate: validating inputs for %q: %w", "postgres", joined)}
	srv := newAPIServer(registry.NewFakeRegistry(postgresSpec()), stub)

	rec := doJSON(t, srv, http.MethodPost, "/api/scaffold",
		`{"moduleType": "postgres", "instanceName": "orders-db", "inputs": {"size": "XL"}}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /api/scaffold with invalid inputs: got status %d, want %d\nbody: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}

	var got struct {
		Errors []string `json:"errors"`
	}
	decodeBody(t, rec, &got)
	if len(got.Errors) != 2 {
		t.Fatalf("422 body: got %d error entries, want 2 (one per joined failure): %v", len(got.Errors), got.Errors)
	}
	if !strings.Contains(got.Errors[0], "size") || !strings.Contains(got.Errors[1], "name") {
		t.Fatalf("422 entries should name the failing inputs: %v", got.Errors)
	}
}

// TestScaffoldChoiceFailuresReturn422 pins the Phase 2 step 4 status mapping:
// the two choice caller-fault sentinels behave exactly like the existing
// validation sentinels — 422 with one entry per joined failure.
func TestScaffoldChoiceFailuresReturn422(t *testing.T) {
	joined := errors.Join(
		fmt.Errorf("%q: %q: %w", "size", "medium", validate.ErrUnknownChoice),
		fmt.Errorf("%q: also set by choice %q option %q: %w", "cpu", "size", "small", validate.ErrChoiceConflict),
	)
	stub := &stubScaffolder{err: fmt.Errorf("orchestrate: validating inputs for %q: %w", "cloud-run", joined)}
	srv := newAPIServer(registry.NewFakeRegistry(choiceSpec()), stub)

	rec := doJSON(t, srv, http.MethodPost, "/api/scaffold",
		`{"moduleType": "cloud-run", "instanceName": "api", "inputs": {"size": "medium", "cpu": "2"}}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /api/scaffold with choice failures: got status %d, want %d\nbody: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	var got struct {
		Errors []string `json:"errors"`
	}
	decodeBody(t, rec, &got)
	if len(got.Errors) != 2 {
		t.Fatalf("422 body: got %d error entries, want 2: %v", len(got.Errors), got.Errors)
	}
}

// TestScaffoldSpecBugReturns500Not422 pins the session-9 firewall at the HTTP
// boundary: an ErrSpecInvalid failure is the platform team's bug, so the
// developer must see a 500 — never a 422 blaming their request.
func TestScaffoldSpecBugReturns500Not422(t *testing.T) {
	stub := &stubScaffolder{err: fmt.Errorf("orchestrate: validating inputs for %q: %w", "cloud-run",
		fmt.Errorf("%q option %q expands to undeclared or choice input %q: %w", "size", "small", "disk", validate.ErrSpecInvalid))}
	srv := newAPIServer(registry.NewFakeRegistry(choiceSpec()), stub)

	rec := doJSON(t, srv, http.MethodPost, "/api/scaffold",
		`{"moduleType": "cloud-run", "instanceName": "api", "inputs": {"size": "small"}}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("POST /api/scaffold with spec bug: got status %d, want %d (a platform spec bug must never be blamed on the caller)\nbody: %s",
			rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
}

func TestScaffoldUnknownModuleReturns422(t *testing.T) {
	stub := &stubScaffolder{err: fmt.Errorf("orchestrate: resolving module %q: %w", "nosuch", registry.ErrModuleNotFound)}
	srv := newAPIServer(registry.NewFakeRegistry(), stub)

	rec := doJSON(t, srv, http.MethodPost, "/api/scaffold",
		`{"moduleType": "nosuch", "instanceName": "x", "inputs": {}}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /api/scaffold with unknown module: got status %d, want %d\nbody: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	var got struct {
		Errors []string `json:"errors"`
	}
	decodeBody(t, rec, &got)
	if len(got.Errors) != 1 || !strings.Contains(got.Errors[0], "nosuch") {
		t.Fatalf("422 body for unknown module: got %v, want one entry naming the module", got.Errors)
	}
}

func TestScaffoldMalformedJSONReturns400(t *testing.T) {
	stub := &stubScaffolder{}
	srv := newAPIServer(registry.NewFakeRegistry(postgresSpec()), stub)

	rec := doJSON(t, srv, http.MethodPost, "/api/scaffold", `{"moduleType":`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/scaffold with malformed JSON: got status %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if stub.calls != 0 {
		t.Fatalf("scaffolder must not run on malformed JSON; called %d times", stub.calls)
	}
}

func TestScaffoldMissingRequiredFieldsReturns400(t *testing.T) {
	cases := map[string]string{
		"missing moduleType":   `{"instanceName": "orders-db", "inputs": {}}`,
		"missing instanceName": `{"moduleType": "postgres", "inputs": {}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			stub := &stubScaffolder{}
			srv := newAPIServer(registry.NewFakeRegistry(postgresSpec()), stub)

			rec := doJSON(t, srv, http.MethodPost, "/api/scaffold", body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("got status %d, want %d\nbody: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if stub.calls != 0 {
				t.Fatalf("scaffolder must not run when required fields are missing; called %d times", stub.calls)
			}
		})
	}
}

func TestScaffoldIdempotentNoOpReturns200ChangedFalse(t *testing.T) {
	stub := &stubScaffolder{result: orchestrate.Result{Changed: false}}
	srv := newAPIServer(registry.NewFakeRegistry(postgresSpec()), stub)

	rec := doJSON(t, srv, http.MethodPost, "/api/scaffold",
		`{"moduleType": "postgres", "instanceName": "orders-db", "inputs": {"name": "orders-db"}}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/scaffold no-op: got status %d, want %d\nbody: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		Changed bool `json:"changed"`
	}
	decodeBody(t, rec, &got)
	if got.Changed {
		t.Fatalf("no-op response: got changed=true, want changed=false")
	}
}

func TestScaffoldPRFailureAfterPushReturns502WithBranch(t *testing.T) {
	// The push-succeeds/PR-fails seam: the orchestrator reports the pushed
	// branch alongside the error so it is not lost. The API surfaces it as 502.
	stub := &stubScaffolder{
		result: orchestrate.Result{Changed: true, Branch: "weave/add-orders-db"},
		err:    errors.New(`orchestrate: opening pull request for pushed branch "weave/add-orders-db": bitbucket: 503`),
	}
	srv := newAPIServer(registry.NewFakeRegistry(postgresSpec()), stub)

	rec := doJSON(t, srv, http.MethodPost, "/api/scaffold",
		`{"moduleType": "postgres", "instanceName": "orders-db", "inputs": {"name": "orders-db"}}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("POST /api/scaffold with PR-after-push failure: got status %d, want %d\nbody: %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
	var got struct {
		Error  string `json:"error"`
		Branch string `json:"branch"`
	}
	decodeBody(t, rec, &got)
	if got.Branch != "weave/add-orders-db" {
		t.Fatalf("502 body must carry the pushed branch: got %+v", got)
	}
	if got.Error == "" {
		t.Fatalf("502 body must carry the error message: got %+v", got)
	}
}

func TestScaffoldInfrastructureFailureReturns500(t *testing.T) {
	stub := &stubScaffolder{err: errors.New("orchestrate: cloning https://bitbucket.org/acme/infra.git: connection refused")}
	srv := newAPIServer(registry.NewFakeRegistry(postgresSpec()), stub)

	rec := doJSON(t, srv, http.MethodPost, "/api/scaffold",
		`{"moduleType": "postgres", "instanceName": "orders-db", "inputs": {"name": "orders-db"}}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("POST /api/scaffold with infrastructure failure: got status %d, want %d\nbody: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	var got struct {
		Error string `json:"error"`
	}
	decodeBody(t, rec, &got)
	if got.Error == "" {
		t.Fatalf("500 body must carry an error message; body: %s", rec.Body.String())
	}
}
