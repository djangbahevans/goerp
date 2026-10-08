package registry

import (
	"maps"
	"slices"

	"github.com/djangbahevans/goerp/internal/engine/cronspec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/rs/zerolog/log"
)

// CronEntry is one module cron job with its schedule parsed.
type CronEntry struct {
	Module   string
	Job      manifest.CronJob
	Schedule cronspec.Schedule
}

// CronRegistry indexes the cron_jobs of every loaded module.
type CronRegistry struct {
	entries []CronEntry
}

// Entries returns the cron jobs ordered by module, then declaration order.
// Callers must treat the slice as read-only.
func (r *CronRegistry) Entries() []CronEntry {
	if r == nil {
		return nil
	}
	return r.entries
}

// buildCronRegistry skips failed modules, like every other derived index,
// and a cron job whose schedule does not parse (manifest validation rejects
// one at load, so this is unreachable for a module that loaded).
func buildCronRegistry(modules map[string]*module.LoadedModule) *CronRegistry {
	reg := &CronRegistry{}
	for _, name := range slices.Sorted(maps.Keys(modules)) {
		if modules[name].Status == module.StatusFailed {
			continue
		}
		for _, job := range modules[name].Manifest.CronJobs {
			schedule, err := cronspec.Parse(job.Schedule)
			if err != nil {
				log.Error().Err(err).Str("module", name).Str("cron_job", job.Name).Msg("cron registry: skipping cron job with an invalid schedule")
				continue
			}
			reg.entries = append(reg.entries, CronEntry{Module: name, Job: job, Schedule: schedule})
		}
	}
	return reg
}
