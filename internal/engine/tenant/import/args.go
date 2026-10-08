// Package tenantimport restores an encrypted tenant-export archive into a new tenant with
// per-module checkpoints.
package tenantimport

import (
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/riverqueue/river"
)

// Args references the encrypted archive in object storage. DecryptionKey is encrypted
// before the job is inserted because River persists arguments verbatim.
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
