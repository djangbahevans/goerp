// Package tenantexport implements goerp#156, "goerp tenant export" —
// an AES-256-GCM-encrypted archive of a tenant's schema and data
// (cli-reference.md §5), resumable per-module via
// internal/engine/checkpoint (goerp#265), with any field carrying a
// restrictive .Access() rule (goerp#264) excluded entirely.
package tenantexport

import (
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/riverqueue/river"
)

// Args is the River job Worker (job.go) runs. Include/Exclude scope
// which modules are exported — cli-reference.md §5's `--include`/
// `--exclude` flags — mutually exclusive in practice (the CLI only ever
// sets one), but both threaded through so the admin API's own
// `{include, exclude}` request body needs no extra validation here.
type Args struct {
	TenantID   string
	TenantSlug string
	Include    []string
	Exclude    []string
}

func (Args) Kind() string { return "tenant.export" }

func (Args) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobqueue.QueueAdmin}
}

// Result is what Worker.Work records via river.RecordOutput
// (adminapi/jobs.go's jobDetailView.Output surfaces it back to a polling
// CLI) — the only place the one-time decryption key is ever returned.
type Result struct {
	DownloadURL   string `json:"download_url"`
	Checksum      string `json:"checksum_sha256"`
	DecryptionKey string `json:"decryption_key"`
}
