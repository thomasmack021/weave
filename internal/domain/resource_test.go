package domain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/thomasmack021/weave/internal/fs"
	"github.com/thomasmack021/weave/internal/registry"
)

// cloudRunSpec is a minimal cloud-run module spec with a single input whose
// TfvarsKey ("service_name") and ModuleArg ("name") differ, exercising the
// Name → TfvarsKey / ModuleArg bridging the Service must perform.
func cloudRunSpec() registry.ModuleSpec {
	return registry.ModuleSpec{
		Name:        "cloud-run",
		DisplayName: "GCP Cloud Run Service",
		Source:      "git::https://github.com/acme/iac-modules.git//modules/cloud-run",
		Version:     "v2.4.0",
		Inputs: []registry.InputSpec{
			{
				Name:      "service_name",
				Type:      "string",
				Required:  true,
				TfvarsKey: "service_name",
				ModuleArg: "name",
			},
		},
	}
}

// newServiceWithCloudRun wires a Service over MemMapFs with a FakeRegistry that
// knows the cloud-run module.
func newServiceWithCloudRun(t *testing.T) (*Service, afero.Fs) {
	t.Helper()
	mem := afero.NewMemMapFs()
	ws := fs.NewWorkspace(mem, "dev")
	svc := NewService(registry.NewFakeRegistry(cloudRunSpec()), ws, "dev")
	return svc, mem
}

// findChange returns the FileChange for the named file, or nil.
func findChange(cs ChangeSet, name string) *FileChange {
	for i := range cs.Files {
		if cs.Files[i].Path == name {
			return &cs.Files[i]
		}
	}
	return nil
}

func TestService_AddResource_HappyPath(t *testing.T) {
	svc, mem := newServiceWithCloudRun(t)
	ctx := context.Background()

	// Day 1 must exist before Day 2.
	if _, err := svc.Scaffold(ctx, ScaffoldRequest{ProjectID: "dev-123", StatePrefix: "weave/dev"}); err != nil {
		t.Fatalf("setup scaffold: %v", err)
	}

	changes, err := svc.AddResource(ctx, AddResourceRequest{
		ModuleType:   "cloud-run",
		InstanceName: "api",
		Inputs:       map[string]string{"service_name": "api"},
	})
	if err != nil {
		t.Fatalf("AddResource returned unexpected error: %v", err)
	}
	if !changes.Changed() {
		t.Errorf("changes.Changed() = false, want true")
	}

	mainTF := mustReadString(t, mem, "terraform/env/dev/main.tf")
	if !strings.Contains(mainTF, `module "api"`) {
		t.Errorf("main.tf missing module block, got:\n%s", mainTF)
	}

	tfvars := mustReadString(t, mem, "terraform/env/dev/dev.tfvars")
	if !strings.Contains(tfvars, `service_name = "api"`) {
		t.Errorf("dev.tfvars missing service_name variable, got:\n%s", tfvars)
	}

	// pipeline.yaml already has the step from Day 1, so it must be unchanged.
	pipeChange := findChange(changes, PipelineFile)
	if pipeChange == nil {
		t.Fatalf("ChangeSet missing %s entry", PipelineFile)
	}
	if pipeChange.Action != ActionUnchanged {
		t.Errorf("%s action = %q, want %q", PipelineFile, pipeChange.Action, ActionUnchanged)
	}
}

// choiceCloudRunSpec extends cloudRunSpec with a required `type: choice` input
// whose options expand to the declared cpu/memory inputs — the Phase 2
// t-shirt-size feature. Mirrors the validate-package choiceSpec fixture.
func choiceCloudRunSpec() registry.ModuleSpec {
	spec := cloudRunSpec()
	spec.Inputs = append(spec.Inputs,
		registry.InputSpec{
			Name:      "cpu",
			Type:      "number",
			Required:  false,
			TfvarsKey: "cpu",
			ModuleArg: "cpu",
		},
		registry.InputSpec{
			Name:      "memory",
			Type:      "string",
			Required:  false,
			TfvarsKey: "memory",
			ModuleArg: "memory",
		},
		registry.InputSpec{
			Name:     "size",
			Type:     "choice",
			Required: true,
			Options: []registry.OptionSpec{
				{
					Value:     "small",
					Label:     "Small — for prototypes",
					ExpandsTo: map[string]any{"cpu": 1, "memory": "512Mi"},
				},
				{
					Value:     "large",
					Label:     "Large — production workloads",
					ExpandsTo: map[string]any{"cpu": 4, "memory": "2Gi"},
				},
			},
		},
	)
	return spec
}

// TestService_AddResource_ChoiceExpandsEndToEnd is the Phase 2 step 3 proof:
// a choice input must land in the generated files ONLY as its expansion —
// concrete tfvars under the targets' TfvarsKeys and module args under their
// ModuleArgs — while the choice key itself appears in no generated file. The
// domain layer consumes validate.Inputs output unchanged, so this is expected
// to pass without any domain code change; the test pins that property.
func TestService_AddResource_ChoiceExpandsEndToEnd(t *testing.T) {
	mem := afero.NewMemMapFs()
	ws := fs.NewWorkspace(mem, "dev")
	svc := NewService(registry.NewFakeRegistry(choiceCloudRunSpec()), ws, "dev")
	ctx := context.Background()

	if _, err := svc.Scaffold(ctx, ScaffoldRequest{ProjectID: "dev-123", StatePrefix: "weave/dev"}); err != nil {
		t.Fatalf("setup scaffold: %v", err)
	}

	changes, err := svc.AddResource(ctx, AddResourceRequest{
		ModuleType:   "cloud-run",
		InstanceName: "api",
		Inputs: map[string]string{
			"service_name": "api",
			"size":         "small",
		},
	})
	if err != nil {
		t.Fatalf("AddResource returned unexpected error: %v", err)
	}
	if !changes.Changed() {
		t.Fatalf("changes.Changed() = false, want true")
	}

	mainTF := mustReadString(t, mem, "terraform/env/dev/main.tf")
	tfvars := mustReadString(t, mem, "terraform/env/dev/dev.tfvars")
	pipe := mustReadString(t, mem, "terraform/env/dev/pipeline.yaml")

	// The expansion must land under the targets' tfvars keys...
	if !strings.Contains(tfvars, `cpu = 1`) {
		t.Errorf("dev.tfvars missing expanded cpu variable, got:\n%s", tfvars)
	}
	if !strings.Contains(tfvars, `memory = "512Mi"`) {
		t.Errorf("dev.tfvars missing expanded memory variable, got:\n%s", tfvars)
	}
	// ...and as module args in the module block.
	if !strings.Contains(mainTF, "cpu") || !strings.Contains(mainTF, "memory") {
		t.Errorf("main.tf missing expanded module args, got:\n%s", mainTF)
	}

	// The virtual choice key must appear in NO generated file.
	for name, content := range map[string]string{
		"main.tf":       mainTF,
		"dev.tfvars":    tfvars,
		"pipeline.yaml": pipe,
	} {
		if strings.Contains(content, "size") {
			t.Errorf("%s leaked the virtual choice key %q:\n%s", name, "size", content)
		}
	}
}

func TestService_AddResource_Idempotent(t *testing.T) {
	svc, _ := newServiceWithCloudRun(t)
	ctx := context.Background()

	if _, err := svc.Scaffold(ctx, ScaffoldRequest{ProjectID: "dev-123", StatePrefix: "weave/dev"}); err != nil {
		t.Fatalf("setup scaffold: %v", err)
	}

	req := AddResourceRequest{
		ModuleType:   "cloud-run",
		InstanceName: "api",
		Inputs:       map[string]string{"service_name": "api"},
	}

	if _, err := svc.AddResource(ctx, req); err != nil {
		t.Fatalf("first AddResource returned unexpected error: %v", err)
	}

	changes, err := svc.AddResource(ctx, req)
	if err != nil {
		t.Fatalf("second AddResource returned unexpected error: %v", err)
	}
	if changes.Changed() {
		t.Errorf("changes.Changed() = true on re-run, want false")
	}
	if len(changes.Files) == 0 {
		t.Errorf("expected ChangeSet to list files as unchanged, got none")
	}
	for _, f := range changes.Files {
		if f.Action != ActionUnchanged {
			t.Errorf("file %s action = %q, want %q", f.Path, f.Action, ActionUnchanged)
		}
	}
}

// TestService_AddResource_ErrModuleNotFound asserts that resolving an unknown
// module fails fast with ErrModuleNotFound and leaves the workspace pristine.
func TestService_AddResource_ErrModuleNotFound(t *testing.T) {
	svc, mem := newTestService(t) // empty registry
	ctx := context.Background()

	_, err := svc.AddResource(ctx, AddResourceRequest{
		ModuleType:   "nonexistent",
		InstanceName: "api",
		Inputs:       map[string]string{"service_name": "api"},
	})
	if err == nil {
		t.Fatalf("AddResource returned nil error, want ErrModuleNotFound")
	}
	if !errors.Is(err, registry.ErrModuleNotFound) {
		t.Errorf("error = %v, want it to wrap registry.ErrModuleNotFound", err)
	}

	for _, name := range []string{"main.tf", "dev.tfvars", "pipeline.yaml"} {
		path := "terraform/env/dev/" + name
		exists, err := afero.Exists(mem, path)
		if err != nil {
			t.Fatalf("checking %s: %v", path, err)
		}
		if exists {
			t.Errorf("workspace not pristine: %s was written despite a failed resolve", path)
		}
	}
}

// TestService_AddResource_ValidationError asserts that an invalid request (here,
// a missing required input) fails before any write, leaving the Day 1 baseline
// files byte-for-byte unchanged.
func TestService_AddResource_ValidationError(t *testing.T) {
	svc, mem := newServiceWithCloudRun(t)
	ctx := context.Background()

	if _, err := svc.Scaffold(ctx, ScaffoldRequest{ProjectID: "dev-123", StatePrefix: "weave/dev"}); err != nil {
		t.Fatalf("setup scaffold: %v", err)
	}

	// Snapshot the baseline state immediately after Day 1.
	before := map[string]string{}
	for _, name := range []string{"main.tf", "dev.tfvars", "pipeline.yaml"} {
		path := "terraform/env/dev/" + name
		before[path] = mustReadString(t, mem, path)
	}

	// Required input "service_name" is missing.
	_, err := svc.AddResource(ctx, AddResourceRequest{
		ModuleType:   "cloud-run",
		InstanceName: "api",
		Inputs:       map[string]string{},
	})
	if err == nil {
		t.Fatalf("AddResource returned nil error, want a validation error")
	}

	for path, want := range before {
		got := mustReadString(t, mem, path)
		if got != want {
			t.Errorf("%s was mutated despite validation failure\n--- before ---\n%s\n--- after ---\n%s", path, want, got)
		}
	}
}
