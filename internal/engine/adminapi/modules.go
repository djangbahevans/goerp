// Package adminapi's module routes (goerp#468) wrap moduleinstall's
// engine-side install orchestration — install is async (§11a), enqueuing
// a River job via ModuleInstaller the same way schema.go/tenant.go's own
// mutating routes do.
package adminapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/djangbahevans/goerp/internal/engine/moduleinstall"
)

type ModulesDeps struct {
	Install ModuleInstaller
}

// ModuleInstaller is satisfied by *moduleinstall.Installer.
type ModuleInstaller interface {
	StartInstall(ctx context.Context, pkg []byte) (jobID string, err error)
}

func RegisterModuleRoutes(mux *http.ServeMux, deps ModulesDeps) {
	h := &moduleHandlers{deps: deps}
	mux.HandleFunc("POST /admin/modules/install", h.install)
}

type moduleHandlers struct {
	deps ModulesDeps
}

func (h *moduleHandlers) install(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "could not read request body")
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be a .erp package")
		return
	}
	// cli-reference.md documents {"registry_ref": "name@version"} as a
	// second accepted body shape — not yet supported (goerp#468's
	// "Explicitly out of scope": the module registry artifact pipeline,
	// backlog #563, doesn't exist to resolve one against). A quick,
	// friendlier check here beats letting it fall through to zip parsing's
	// generic "not a valid archive" error.
	if looksLikeJSONObject(body) {
		writeError(w, http.StatusNotImplemented, "not_implemented", `install by "registry_ref" is not yet supported — submit the .erp package binary directly, tracked as goerp#563`)
		return
	}

	jobID, err := h.deps.Install.StartInstall(r.Context(), body)
	if err != nil {
		if errors.Is(err, moduleinstall.ErrInvalidPackage) {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	writeData(w, http.StatusAccepted, struct {
		JobID string `json:"job_id"`
	}{JobID: jobID})
}

func looksLikeJSONObject(body []byte) bool {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == '{'
}
