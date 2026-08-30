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

// ModuleReloader is satisfied by an adapter over
// hotreload.Coordinator.OnModuleBytesChanged (goerp#452) — unlike
// ModuleInstaller, it has no job to report: hot reload's coordination
// entry points are void-returning and self-logging by design (see
// hotreload.Coordinator's own doc comments), so TriggerReload only ever
// hands the package off.
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

// reload accepts a new .erp package for an already-installed module and
// hands it off to ModuleReloader without blocking the response on it —
// unlike install, there is no River job wrapping hot reload (goerp#452
// only builds the trigger/coordination scaffolding; the leader/follower
// work itself is goerp#467 and its follower-path counterpart's own
// scope), so 202 here only means "accepted for processing," not "queued
// as a trackable job." The coordinator logs its own outcome — see
// hotreload.Coordinator's doc comments — including the no-op case when
// the uploaded version isn't newer than what's already installed.
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

	// Detached from the request's own context (which is cancelled the
	// moment this handler returns) — deliberately not tracked against any
	// shutdown-aware WaitGroup, unlike Coordinator's own Start/Stop
	// lifecycle for the other three trigger sources. Acceptable today
	// because stubHotReloadLeader/Follower (goerp#452's own placeholders)
	// return almost immediately; once goerp#467/#490 replace them with
	// real, slow work, an in-flight reload triggered here could still be
	// running after Engine.Shutdown returns. Revisit then, not before —
	// solving it now would mean guessing at goerp#467/#490's own shape.
	go h.deps.Reload.TriggerReload(context.Background(), name, body)

	writeData(w, http.StatusAccepted, struct {
		Module string `json:"module"`
	}{Module: name})
}

func looksLikeJSONObject(body []byte) bool {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == '{'
}
