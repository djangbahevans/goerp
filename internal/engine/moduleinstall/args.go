package moduleinstall

import (
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/riverqueue/river"
)

// Args is the River job Worker (worker.go) runs. PackagePath is where
// Installer.StartInstall persisted the submitted .erp package on local
// disk, under the engine's own module directory — module packages are
// always local to the one engine process that loads them (no cross-
// machine portability requirement the way a tenant export archive has),
// so there's no need for the object-storage upload-then-reference
// indirection tenant import/export use for their own binary payloads.
// Persisting under the module directory (rather than a scratch location)
// also means a subsequent engine restart's moduleboot.Discover picks the
// installed module back up the same way it does any other — install
// wouldn't otherwise survive a restart.
type Args struct {
	PackagePath string
}

func (Args) Kind() string { return "module.install" }

func (Args) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobqueue.QueueAdmin}
}

// TenantResult is one tenant's schema-sync outcome within a Result.
type TenantResult struct {
	Tenant string `json:"tenant"`
	Error  string `json:"error,omitempty"`
}

// Result is what Worker.Work records via river.RecordOutput
// (adminapi/jobs.go's jobDetailView.Output surfaces it back to a polling
// CLI). The module reaching READY does not imply every tenant synced
// cleanly — Failed lists exactly which ones didn't and why, without that
// blocking the others or the module overall (module_schema_versions
// itself already carries the authoritative per-tenant record; this is a
// point-in-time summary of the one install run that produced it).
type Result struct {
	Module          string         `json:"module"`
	Version         string         `json:"version"`
	Succeeded       []string       `json:"succeeded_tenants"`
	Failed          []TenantResult `json:"failed_tenants"`
	WorkflowWorkers string         `json:"workflow_worker_error,omitempty"`
}
