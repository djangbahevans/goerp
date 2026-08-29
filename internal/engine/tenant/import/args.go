// Package tenantimport implements goerp#157, "goerp tenant import" —
// restoring a goerp#156 tenant-export archive as a brand-new tenant,
// resumable per-module via internal/engine/checkpoint (goerp#265), the
// same mechanism tenantexport already uses.
package tenantimport

import (
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/riverqueue/river"
)

// Args is the River job Worker (worker.go) runs. InputRef is the
// object-storage key the admin API's upload endpoint returned for the
// still-encrypted archive the CLI uploaded from the operator's local disk.
// DecryptionKey is rowcrypt-encrypted before Importer.StartImport inserts
// this job (goerp#450) — River persists Args as-is in river_job.args for
// the life of the job row, so the archive's own decryption key never sits
// there in plaintext.
type Args struct {
	NewSlug       string
	InputRef      string
	DecryptionKey string
}

func (Args) Kind() string { return "tenant.import" }

func (Args) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobqueue.QueueAdmin}
}

// Result is what Worker.Work records via river.RecordOutput
// (adminapi/jobs.go's jobDetailView.Output surfaces it back to a polling
// CLI).
type Result struct {
	TenantID        string   `json:"tenant_id"`
	TenantSlug      string   `json:"tenant_slug"`
	ModulesImported []string `json:"modules_imported"`
}
