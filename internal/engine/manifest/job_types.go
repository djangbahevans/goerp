package manifest

import "cmp"

// defaultJobTimeoutSeconds is a job type's timeout_seconds when omitted
// (manifest-spec.md §15).
const defaultJobTimeoutSeconds = 300

// EffectiveTimeoutSeconds is the declared timeout, or the default when omitted.
func (j JobType) EffectiveTimeoutSeconds() int {
	return cmp.Or(j.TimeoutSeconds, defaultJobTimeoutSeconds)
}
