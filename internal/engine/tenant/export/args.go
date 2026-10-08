// Package tenantexport creates encrypted tenant schema/data archives with per-module
// checkpoints and excludes fields with restrictive access rules.
package tenantexport

import (
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/riverqueue/river"
)

// Args selects modules for export through Include and Exclude filters supplied by the
// admin API.
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

// Result carries the archive location and an encrypted decryption key in persisted job
// output. DecryptOutput reveals the key to an authenticated admin poller.
type Result struct {
	DownloadURL   string `json:"download_url"`
	Checksum      string `json:"checksum_sha256"`
	DecryptionKey string `json:"decryption_key"`
}
