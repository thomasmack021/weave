package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/thomasmack021/weave/internal/orchestrate"
	"github.com/thomasmack021/weave/internal/store"
)

// fakeStore is an in-memory usecase.Store for unit tests.
type fakeStore struct {
	useCases map[string]store.UseCase         // key -> use case
	roles    map[string]map[string]store.Role // useCaseID -> subject -> role
	created  []store.UseCase
	members  []store.Membership
	grants   []store.GroupGrant
	upserts  map[string]string // subject -> id
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		useCases: map[string]store.UseCase{},
		roles:    map[string]map[string]store.Role{},
		upserts:  map[string]string{},
	}
}

func (f *fakeStore) withUseCase(uc store.UseCase) *fakeStore { f.useCases[uc.Key] = uc; return f }
func (f *fakeStore) grant(useCaseID, subject string, role store.Role) *fakeStore {
	if f.roles[useCaseID] == nil {
		f.roles[useCaseID] = map[string]store.Role{}
	}
	f.roles[useCaseID][subject] = role
	return f
}

func (f *fakeStore) GetUseCaseByKey(_ context.Context, key string) (store.UseCase, error) {
	uc, ok := f.useCases[key]
	if !ok {
		return store.UseCase{}, store.ErrNotFound
	}
	return uc, nil
}

func (f *fakeStore) EffectiveRole(_ context.Context, useCaseID string, p store.Principal) (store.Role, bool, error) {
	if r, ok := f.roles[useCaseID][p.Subject]; ok {
		return r, true, nil
	}
	return "", false, nil
}

func (f *fakeStore) ListUseCasesForPrincipal(_ context.Context, p store.Principal) ([]store.UseCase, error) {
	var out []store.UseCase
	for _, uc := range f.useCases {
		if _, ok := f.roles[uc.ID][p.Subject]; ok {
			out = append(out, uc)
		}
	}
	return out, nil
}

func (f *fakeStore) CreateUseCase(_ context.Context, uc store.UseCase) (store.UseCase, error) {
	uc.ID = "id-" + uc.Key
	f.created = append(f.created, uc)
	f.useCases[uc.Key] = uc
	return uc, nil
}
func (f *fakeStore) UpsertUser(_ context.Context, subject, _ string) (store.User, error) {
	id := "user-" + subject
	f.upserts[subject] = id
	return store.User{ID: id, Subject: subject}, nil
}
func (f *fakeStore) AddMembership(_ context.Context, m store.Membership) error {
	f.members = append(f.members, m)
	return nil
}
func (f *fakeStore) AddGroupGrant(_ context.Context, g store.GroupGrant) error {
	f.grants = append(f.grants, g)
	return nil
}

// fakeRunner records whether it ran, standing in for a real orchestrator.
type fakeRunner struct {
	ran    bool
	result orchestrate.Result
	err    error
}

func (r *fakeRunner) Run(context.Context, orchestrate.Request) (orchestrate.Result, error) {
	r.ran = true
	return r.result, r.err
}
func (r *fakeRunner) InitWorkspace(context.Context, orchestrate.InitRequest) (orchestrate.Result, error) {
	r.ran = true
	return r.result, r.err
}

// fakeFactory hands out a fakeRunner and records the use case it was built for.
type fakeFactory struct {
	runner    *fakeRunner
	builtFor  string // use case id
	buildErr  error
	callCount int
}

func (f *fakeFactory) For(_ context.Context, uc store.UseCase) (Runner, error) {
	f.callCount++
	f.builtFor = uc.ID
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	return f.runner, nil
}

func newService(fs *fakeStore, ff *fakeFactory, admins ...string) *Service {
	return NewService(fs, ff, admins)
}

const devSubject = "dev@acme.example"

func devPrincipal() store.Principal { return store.Principal{Subject: devSubject} }

// --- Scaffold RBAC ---

func TestScaffold_MemberDeveloperSucceeds(t *testing.T) {
	uc := store.UseCase{ID: "uc1", Key: "payments", RepoURL: "u", Env: "prod"}
	fs := newFakeStore().withUseCase(uc).grant("uc1", devSubject, store.RoleDeveloper)
	ff := &fakeFactory{runner: &fakeRunner{result: orchestrate.Result{Changed: true, Branch: "b", PRURL: "url"}}}
	svc := newService(fs, ff)

	res, err := svc.Scaffold(context.Background(), devPrincipal(), "payments", orchestrate.Request{ModuleType: "cloud-run", InstanceName: "x"})
	if err != nil {
		t.Fatalf("Scaffold error = %v, want nil", err)
	}
	if !res.Changed || res.PRURL != "url" {
		t.Errorf("Scaffold result = %+v, want the runner's result", res)
	}
	if !ff.runner.ran {
		t.Error("runner should have run for an authorized developer")
	}
	if ff.builtFor != "uc1" {
		t.Errorf("orchestrator built for %q, want the resolved use case id", ff.builtFor)
	}
}

// TestScaffold_NonMemberForbiddenBeforeOrchestration is the key RBAC guard: a
// principal with no access is rejected BEFORE any orchestrator is built — the
// fail-before-mutate boundary extended to tenancy.
func TestScaffold_NonMemberForbiddenBeforeOrchestration(t *testing.T) {
	uc := store.UseCase{ID: "uc1", Key: "payments"}
	fs := newFakeStore().withUseCase(uc) // no grant for devSubject
	ff := &fakeFactory{runner: &fakeRunner{}}
	svc := newService(fs, ff)

	_, err := svc.Scaffold(context.Background(), devPrincipal(), "payments", orchestrate.Request{})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Scaffold error = %v, want ErrForbidden", err)
	}
	if ff.callCount != 0 {
		t.Errorf("orchestrator factory called %d times, want 0 (deny before orchestration)", ff.callCount)
	}
	if ff.runner.ran {
		t.Error("runner must not run for a forbidden request")
	}
}

func TestScaffold_UnknownUseCaseIsNotFound(t *testing.T) {
	fs := newFakeStore()
	ff := &fakeFactory{runner: &fakeRunner{}}
	svc := newService(fs, ff)

	_, err := svc.Scaffold(context.Background(), devPrincipal(), "ghost", orchestrate.Request{})
	if !errors.Is(err, ErrUseCaseNotFound) {
		t.Fatalf("Scaffold error = %v, want ErrUseCaseNotFound", err)
	}
}

// TestScaffold_GlobalAdminBypassesMembership: a bootstrap admin can act on any
// use case without an explicit membership.
func TestScaffold_GlobalAdminBypassesMembership(t *testing.T) {
	uc := store.UseCase{ID: "uc1", Key: "payments"}
	fs := newFakeStore().withUseCase(uc) // no membership
	ff := &fakeFactory{runner: &fakeRunner{result: orchestrate.Result{Changed: true}}}
	svc := newService(fs, ff, "boss@acme.example")

	_, err := svc.Scaffold(context.Background(), store.Principal{Subject: "boss@acme.example"}, "payments", orchestrate.Request{})
	if err != nil {
		t.Fatalf("Scaffold (global admin) error = %v, want nil", err)
	}
	if !ff.runner.ran {
		t.Error("global admin should be allowed to scaffold")
	}
}

// TestGlobalAdminByGroup: bootstrap admins may be named by group, matching a
// principal's forwarded groups.
func TestScaffold_GlobalAdminByGroup(t *testing.T) {
	uc := store.UseCase{ID: "uc1", Key: "payments"}
	fs := newFakeStore().withUseCase(uc)
	ff := &fakeFactory{runner: &fakeRunner{}}
	svc := newService(fs, ff, "platform-admins") // a group name

	p := store.Principal{Subject: "someone@acme.example", Groups: []string{"platform-admins"}}
	if _, err := svc.Scaffold(context.Background(), p, "payments", orchestrate.Request{}); err != nil {
		t.Fatalf("Scaffold error = %v, want nil (admin by group)", err)
	}
}

// --- Admin management RBAC ---

func TestCreateUseCase_RequiresGlobalAdmin(t *testing.T) {
	fs := newFakeStore()
	ff := &fakeFactory{runner: &fakeRunner{}}
	svc := newService(fs, ff, "boss@acme.example")

	// A non-admin cannot create.
	if _, err := svc.CreateUseCase(context.Background(), devPrincipal(), store.UseCase{Key: "new"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateUseCase(non-admin) error = %v, want ErrForbidden", err)
	}
	// The bootstrap admin can.
	uc, err := svc.CreateUseCase(context.Background(), store.Principal{Subject: "boss@acme.example"}, store.UseCase{Key: "new", RepoURL: "u", Env: "prod"})
	if err != nil {
		t.Fatalf("CreateUseCase(admin) error = %v, want nil", err)
	}
	if uc.ID == "" || len(fs.created) != 1 {
		t.Errorf("CreateUseCase did not persist the use case")
	}
}

func TestAddMember_RequiresUseCaseAdmin(t *testing.T) {
	uc := store.UseCase{ID: "uc1", Key: "payments"}
	fs := newFakeStore().withUseCase(uc).grant("uc1", "admin@acme.example", store.RoleAdmin).grant("uc1", devSubject, store.RoleDeveloper)
	ff := &fakeFactory{runner: &fakeRunner{}}
	svc := newService(fs, ff)

	// A developer on the use case cannot add members.
	if err := svc.AddMember(context.Background(), devPrincipal(), "payments", "new@acme.example", store.RoleDeveloper); !errors.Is(err, ErrForbidden) {
		t.Fatalf("AddMember(developer) error = %v, want ErrForbidden", err)
	}
	// A use-case admin can.
	if err := svc.AddMember(context.Background(), store.Principal{Subject: "admin@acme.example"}, "payments", "new@acme.example", store.RoleDeveloper); err != nil {
		t.Fatalf("AddMember(admin) error = %v, want nil", err)
	}
	if len(fs.members) != 1 || fs.members[0].Role != store.RoleDeveloper {
		t.Errorf("AddMember did not persist the membership: %+v", fs.members)
	}
}

func TestList_ReturnsPrincipalsUseCases(t *testing.T) {
	a := store.UseCase{ID: "uc1", Key: "payments"}
	b := store.UseCase{ID: "uc2", Key: "billing"}
	fs := newFakeStore().withUseCase(a).withUseCase(b).grant("uc1", devSubject, store.RoleDeveloper)
	svc := newService(fs, &fakeFactory{runner: &fakeRunner{}})

	list, err := svc.List(context.Background(), devPrincipal())
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(list) != 1 || list[0].Key != "payments" {
		t.Errorf("List = %v, want only [payments]", list)
	}
}
