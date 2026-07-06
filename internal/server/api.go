package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/thomasmack/weave/internal/orchestrate"
	"github.com/thomasmack/weave/internal/registry"
	"github.com/thomasmack/weave/internal/validate"
)

// moduleDTO is the client-facing projection of a registry.ModuleSpec. It is a
// deliberate subset: internal fields such as the module git Source must never
// reach the browser.
type moduleDTO struct {
	Name        string     `json:"name"`
	DisplayName string     `json:"displayName"`
	Description string     `json:"description"`
	Version     string     `json:"version"`
	Inputs      []inputDTO `json:"inputs"`
}

type inputDTO struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Required    bool        `json:"required"`
	Description string      `json:"description"`
	Options     []optionDTO `json:"options,omitempty"`
	// ManagedByChoice marks an input that some choice option's expansion
	// sets. A wizard must not render it as a free-form field: a direct value
	// would collide with the expansion (ErrChoiceConflict). The flag reveals
	// only that the platform manages the input, never which choice sets what.
	ManagedByChoice bool `json:"managedByChoice"`
}

// optionDTO is the client-facing projection of one choice option: exactly
// what a wizard needs to render a business-language selector. The option's
// expandsTo map is a server-side implementation detail (applied by
// validate.Inputs) and, like the module Source, must never reach the browser.
type optionDTO struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// scaffoldRequest is the only data a client may supply: repo URL, base
// branch, environment, and token come from server config. Unknown body fields
// are ignored by decoding, so config-shaped fields cannot reach the Scaffolder.
type scaffoldRequest struct {
	ModuleType   string            `json:"moduleType"`
	InstanceName string            `json:"instanceName"`
	Inputs       map[string]string `json:"inputs"`
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	specs, err := s.registry.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "listing module catalog: " + err.Error()})
		return
	}

	dtos := make([]moduleDTO, 0, len(specs))
	for _, spec := range specs {
		// An input is choice-managed when any option of any choice input in
		// this module expands into it.
		managed := make(map[string]bool)
		for _, in := range spec.Inputs {
			for _, opt := range in.Options {
				for target := range opt.ExpandsTo {
					managed[target] = true
				}
			}
		}

		inputs := make([]inputDTO, 0, len(spec.Inputs))
		for _, in := range spec.Inputs {
			var options []optionDTO
			for _, opt := range in.Options {
				options = append(options, optionDTO{
					Value:       opt.Value,
					Label:       opt.Label,
					Description: opt.Description,
				})
			}
			inputs = append(inputs, inputDTO{
				Name:            in.Name,
				Type:            in.Type,
				Required:        in.Required,
				Description:     in.Description,
				Options:         options,
				ManagedByChoice: managed[in.Name],
			})
		}
		dtos = append(dtos, moduleDTO{
			Name:        spec.Name,
			DisplayName: spec.DisplayName,
			Description: spec.Description,
			Version:     spec.Version,
			Inputs:      inputs,
		})
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleScaffold(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req scaffoldRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed JSON body: " + err.Error()})
		return
	}
	if req.ModuleType == "" || req.InstanceName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "moduleType and instanceName are required"})
		return
	}

	result, err := s.scaffolder.Run(r.Context(), orchestrate.Request{
		ModuleType:   req.ModuleType,
		InstanceName: req.InstanceName,
		Inputs:       req.Inputs,
	})
	if err != nil {
		// Classification only — the server never re-validates; it maps the
		// orchestrator's sentinel-bearing error chain onto HTTP statuses.
		switch {
		case isRequestError(err):
			writeJSON(w, http.StatusUnprocessableEntity, map[string][]string{"errors": errorMessages(err)})
		case result.Branch != "":
			// Push succeeded but PR creation failed: surface the pushed
			// branch so the caller can recover it instead of losing it.
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error(), "branch": result.Branch})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}

	if !result.Changed {
		writeJSON(w, http.StatusOK, map[string]bool{"changed": false})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"prUrl": result.PRURL, "branch": result.Branch})
}

// isRequestError reports whether err is the caller's fault — an unknown
// module or a spec-validation failure — and therefore maps to 422 rather
// than 5xx. validate.ErrSpecInvalid is deliberately absent: a spec-authoring
// bug is the platform team's fault and must surface as a 500, never a 422.
func isRequestError(err error) bool {
	return errors.Is(err, registry.ErrModuleNotFound) ||
		errors.Is(err, validate.ErrMissingRequired) ||
		errors.Is(err, validate.ErrPatternMismatch) ||
		errors.Is(err, validate.ErrMaxLengthExceeded) ||
		errors.Is(err, validate.ErrInvalidValue) ||
		errors.Is(err, validate.ErrUnknownInput) ||
		errors.Is(err, validate.ErrUnknownChoice) ||
		errors.Is(err, validate.ErrChoiceConflict)
}

// errorMessages flattens err into one message per underlying failure.
// validate.Inputs accumulates failures via errors.Join (wrapped once more by
// the orchestrator); the first multi-error found in the single-unwrap chain
// is expanded recursively so each joined failure becomes its own entry. An
// error chain without a join yields a single entry.
func errorMessages(err error) []string {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if multi, ok := e.(interface{ Unwrap() []error }); ok {
			var msgs []string
			for _, sub := range multi.Unwrap() {
				msgs = append(msgs, errorMessages(sub)...)
			}
			return msgs
		}
	}
	return []string{err.Error()}
}

// writeJSON writes v as the JSON response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Encoding failures at this point cannot be reported to the client (the
	// status line is already written); there is nothing actionable to do.
	_ = json.NewEncoder(w).Encode(v)
}
