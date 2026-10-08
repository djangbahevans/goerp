// These module routes enqueue asynchronous installs and hand reload requests to the hot-
// reload coordinator.
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
	Reload  ModuleReloader
	// ReloadEnabled mirrors GOERP_HOT_RELOAD_ENABLED (default false) —
	// the same flag that gates whether Engine.Start launches
	// hotreload.Coordinator's fsnotify/pub-sub/poll trigger goroutines at
	// all. Without this, POST /admin/modules/{name}/reload would be the
	// one hot-reload trigger source that ran real leader-election
	// coordination regardless of the flag, inconsistent with the other
	// three.
	ReloadEnabled bool
}

// ModuleInstaller is satisfied by *moduleinstall.Installer.
type ModuleInstaller interface {
	StartInstall(ctx context.Context, pkg []byte) (jobID string, err error)
}

// ModuleReloader hands packages to the hot-reload coordinator, which logs outcomes without
// exposing a trackable job.
type ModuleReloader interface {
	TriggerReload(ctx context.Context, moduleName string, data []byte)
}

func RegisterModuleRoutes(mux *http.ServeMux, deps ModulesDeps) {
	h := &moduleHandlers{deps: deps}
	mux.HandleFunc("POST /admin/modules/install", h.install)
	mux.HandleFunc("POST /admin/modules/{name}/reload", h.reload)
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
	// Reject registry references before zip parsing so the caller receives an actionable
	// error.
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

// reload returns 202 when the package is accepted for processing. The coordinator logs its
// outcome without exposing a trackable job.
func (h *moduleHandlers) reload(w http.ResponseWriter, r *http.Request) {
	if !h.deps.ReloadEnabled {
		writeError(w, http.StatusServiceUnavailable, "hot_reload_disabled", "hot reload is disabled (GOERP_HOT_RELOAD_ENABLED=false)")
		return
	}

	name := r.PathValue("name")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "could not read request body")
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be a .erp package")
		return
	}

	// Detach from request cancellation but track reload work through coordinator shutdown
	// so runtime and database closure cannot race it.
	h.deps.Reload.TriggerReload(context.Background(), name, body)

	writeData(w, http.StatusAccepted, struct {
		Module string `json:"module"`
	}{Module: name})
}

func looksLikeJSONObject(body []byte) bool {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == '{'
}
