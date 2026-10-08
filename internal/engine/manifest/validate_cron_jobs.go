package manifest

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/cronspec"
)

// Defaults and limits of a cron_jobs entry (manifest-spec.md §16).
const (
	defaultCronTimeoutSeconds = 3600
	maxCronTimeoutSeconds     = 86400
	defaultCronQueue          = "bulk"
)

var cronQueues = []string{"critical", "default", "bulk", "email", "search"}

// IsEnabledByDefault reports whether the job runs after install: true unless
// the manifest sets enabled_by_default to false.
func (c CronJob) IsEnabledByDefault() bool {
	return c.EnabledByDefault == nil || *c.EnabledByDefault
}

// EffectiveTimeoutSeconds is the declared timeout, or the default when omitted.
func (c CronJob) EffectiveTimeoutSeconds() int {
	return cmp.Or(c.TimeoutSeconds, defaultCronTimeoutSeconds)
}

// EffectiveQueue is the declared queue, or the default when omitted.
func (c CronJob) EffectiveQueue() string {
	return cmp.Or(c.Queue, defaultCronQueue)
}

// validateCronJobs enforces manifest-spec.md §16: required name, label,
// schedule and handler, a name unique within the module, a schedule that
// parses as 5-field cron, a known queue and a timeout of at most 24 hours.
func validateCronJobs(m Manifest) error {
	var violations []string
	seen := make(map[string]bool, len(m.CronJobs))
	for i, job := range m.CronJobs {
		entry := fmt.Sprintf("cron job %q", job.Name)
		if job.Name == "" {
			entry = fmt.Sprintf("cron_jobs[%d]", i)
		}
		reject := func(format string, args ...any) {
			violations = append(violations, entry+": "+fmt.Sprintf(format, args...))
		}

		for field, value := range map[string]string{"name": job.Name, "label": job.Label, "schedule": job.Schedule, "handler": job.Handler} {
			if value == "" {
				reject("%s is required", field)
			}
		}
		if job.Name != "" {
			if seen[job.Name] {
				reject("name must be unique within this manifest's cron_jobs")
			}
			seen[job.Name] = true
		}
		if job.Schedule != "" {
			if _, err := cronspec.Parse(job.Schedule); err != nil {
				reject("%v", err)
			}
		}
		if job.Queue != "" && !slices.Contains(cronQueues, job.Queue) {
			reject("queue %q must be one of %s", job.Queue, strings.Join(cronQueues, ", "))
		}
		if job.TimeoutSeconds < 0 || job.TimeoutSeconds > maxCronTimeoutSeconds {
			reject("timeout_seconds %d must be between 0 and %d", job.TimeoutSeconds, maxCronTimeoutSeconds)
		}
	}

	if len(violations) == 0 {
		return nil
	}
	slices.Sort(violations)
	return errors.New(strings.Join(violations, "; "))
}
