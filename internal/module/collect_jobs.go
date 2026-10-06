package module

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/jobs/def"
)

func init() {
	registerCollector(jobTypesCollector{})
	registerCollector(cronJobsCollector{})
}

// jobTypesCollector generates job_types from jobs.Define declarations and
// engine.HandleJob registrations (manifest-spec.md §15). A provider job
// declares nothing, so it produces no entry.
type jobTypesCollector struct{}

func (jobTypesCollector) Key() string { return "job_types" }

func (jobTypesCollector) Kinds() []string { return []string{def.KindJob, def.KindJobHandler} }

func (jobTypesCollector) Collect(d Declarations, _ ModuleInfo) (any, error) {
	jobs, routing, err := pairHandlers(d, def.KindJob, def.KindJobHandler, "jobs.Define", "engine.HandleJob",
		func(j def.JobDeclaration) string { return j.Name })
	if err != nil {
		return nil, err
	}

	var problems []error
	out := make([]manifest.JobType, 0, len(jobs))
	for _, j := range jobs {
		if j.UniqueBy != "" && j.PayloadStruct && !slices.Contains(j.PayloadFields, j.UniqueBy) {
			problems = append(problems, fmt.Errorf("job %q: jobs.UniqueBy(%q) names no field of its payload type (fields: %v)", j.Name, j.UniqueBy, j.PayloadFields))
		}
		out = append(out, manifest.JobType{
			Name:           j.Name,
			Label:          j.Label,
			Handler:        routing[j.Name],
			Queue:          cmp.Or(j.Queue, def.QueueDefault),
			TimeoutSeconds: j.TimeoutSeconds,
			MaxAttempts:    j.MaxAttempts,
			UniqueBy:       j.UniqueBy,
			Description:    j.Description,
			Priority:       j.Priority,
		})
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	slices.SortFunc(out, func(a, b manifest.JobType) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

// cronJobsCollector generates cron_jobs from jobs.DefineCron declarations and
// engine.HandleCron registrations (manifest-spec.md §16).
type cronJobsCollector struct{}

func (cronJobsCollector) Key() string { return "cron_jobs" }

func (cronJobsCollector) Kinds() []string { return []string{def.KindCron, def.KindCronHandler} }

func (cronJobsCollector) Collect(d Declarations, _ ModuleInfo) (any, error) {
	crons, routing, err := pairHandlers(d, def.KindCron, def.KindCronHandler, "jobs.DefineCron", "engine.HandleCron",
		func(c def.CronDeclaration) string { return c.Name })
	if err != nil {
		return nil, err
	}

	out := make([]manifest.CronJob, 0, len(crons))
	for _, c := range crons {
		enabled := !c.DisabledByDefault
		out = append(out, manifest.CronJob{
			Name:             c.Name,
			Label:            c.Label,
			Schedule:         c.Schedule,
			Handler:          routing[c.Name],
			Description:      c.Description,
			EnabledByDefault: &enabled,
			TimeoutSeconds:   c.TimeoutSeconds,
			Queue:            c.Queue,
		})
	}

	slices.SortFunc(out, func(a, b manifest.CronJob) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

// pairHandlers decodes the definitions of defKind and the handlers of
// handlerKind and checks they correspond one to one by name. It returns the
// definitions and each one's handler routing name. defines and handles name
// the declaring and registering calls in the errors.
func pairHandlers[D any](d Declarations, defKind, handlerKind, defines, handles string, nameOf func(D) string) ([]D, map[string]string, error) {
	defs, err := decodeDeclarations[D](d, defKind)
	if err != nil {
		return nil, nil, err
	}
	handlers, err := decodeDeclarations[def.HandlerDeclaration](d, handlerKind)
	if err != nil {
		return nil, nil, err
	}

	var problems []error
	declared := make(map[string]bool, len(defs))
	for _, definition := range defs {
		name := nameOf(definition)
		if declared[name] {
			problems = append(problems, fmt.Errorf("%q is declared more than once with %s", name, defines))
		}
		declared[name] = true
	}

	routing := make(map[string]string, len(handlers))
	for _, h := range handlers {
		routing[h.Name] = h.Handler
		if !declared[h.Name] {
			problems = append(problems, fmt.Errorf("%s is registered for %q, which the module never declared with %s", handles, h.Name, defines))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		if _, ok := routing[name]; !ok {
			problems = append(problems, fmt.Errorf("%q is declared with %s but has no %s registration", name, defines, handles))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return nil, nil, err
	}
	return defs, routing, nil
}
