// Package job is the job type registry (manifest-spec.md §15) — records
// each module's declared job_types[] and enforces name uniqueness across
// modules. Mirrors route/event/permission/fieldsec's New()+Register()
// shape; queue dispatch itself is separate, later scope.
package job

import (
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

// JobRegistry records which module declared each job type name.
type JobRegistry struct {
	owners map[string]string // job_types[].name -> declaring module name
}

func New() *JobRegistry {
	return &JobRegistry{owners: make(map[string]string)}
}

// Register records moduleName's declared job types, all-or-nothing: a
// name already owned by a different module fails the whole call and
// leaves jobTypes unrecorded. Re-registering the same module is
// idempotent.
func (r *JobRegistry) Register(moduleName string, jobTypes []manifest.JobType) error {
	for _, jt := range jobTypes {
		if owner, ok := r.owners[jt.Name]; ok && owner != moduleName {
			return fmt.Errorf("job: module %q: job type %q already registered by module %q", moduleName, jt.Name, owner)
		}
	}

	for _, jt := range jobTypes {
		r.owners[jt.Name] = moduleName
	}

	return nil
}

// Owner returns the module name that registered jobTypeName, and whether
// it has been registered at all.
func (r *JobRegistry) Owner(jobTypeName string) (string, bool) {
	owner, ok := r.owners[jobTypeName]
	return owner, ok
}
