package module

import (
	"cmp"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/events/def"
)

func init() {
	registerCollector(emitsCollector{})
	registerCollector(subscribesCollector{})
}

// emitsCollector generates emits from events.Define declarations
// (manifest-spec.md §6). An event's owning module is the first segment of its
// name, so a definition of another module's event, which a subscriber makes to
// read the payload fields it needs, declares nothing the module emits.
type emitsCollector struct{}

func (emitsCollector) Key() string { return "emits" }

func (emitsCollector) Kinds() []string { return []string{def.KindEvent} }

func (emitsCollector) Collect(d Declarations, info ModuleInfo) (any, error) {
	events, err := decodeDeclarations[def.EventDeclaration](d, def.KindEvent)
	if err != nil {
		return nil, err
	}

	var problems []error
	var out []manifest.EventDeclaration
	for _, e := range events {
		if owner, _, _ := strings.Cut(e.Name, "."); owner != info.Name {
			continue
		}

		if _, ok := e.PayloadSchema[e.IdempotencyKeyField]; e.IdempotencyKeyField != "" && e.PayloadSchema != nil && !ok {
			problems = append(problems, fmt.Errorf("event %s v%d: events.IdempotencyKeyField(%q) names no field of its payload type", e.Name, e.Version, e.IdempotencyKeyField))
		}

		entry := manifest.EventDeclaration{
			Name:                e.Name,
			Version:             e.Version,
			Description:         e.Description,
			PayloadSchema:       e.PayloadSchema,
			IdempotencyKeyField: e.IdempotencyKeyField,
		}
		if i := slices.IndexFunc(out, func(o manifest.EventDeclaration) bool { return o.Name == e.Name && o.Version == e.Version }); i >= 0 {
			if !reflect.DeepEqual(out[i], entry) {
				problems = append(problems, fmt.Errorf("event %s v%d is declared twice with different descriptions or payloads", e.Name, e.Version))
			}
			continue
		}
		out = append(out, entry)
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	slices.SortFunc(out, func(a, b manifest.EventDeclaration) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Version, b.Version))
	})
	return out, nil
}

// subscribesCollector generates subscribes from engine.Subscribe
// registrations, one entry per registered event version (manifest-spec.md §6).
type subscribesCollector struct{}

func (subscribesCollector) Key() string { return "subscribes" }

func (subscribesCollector) Kinds() []string { return []string{def.KindSubscription} }

func (subscribesCollector) Collect(d Declarations, _ ModuleInfo) (any, error) {
	subs, err := decodeDeclarations[def.SubscriptionDeclaration](d, def.KindSubscription)
	if err != nil {
		return nil, err
	}

	var problems []error
	out := make([]manifest.EventSubscription, 0, len(subs))
	for _, s := range subs {
		if slices.ContainsFunc(out, func(o manifest.EventSubscription) bool { return o.Name == s.Event && o.Version == s.Version }) {
			problems = append(problems, fmt.Errorf("event %s v%d has more than one engine.Subscribe registration", s.Event, s.Version))
			continue
		}
		out = append(out, manifest.EventSubscription{
			Name:                s.Event,
			Version:             s.Version,
			Handler:             s.Handler,
			Async:               s.Async,
			IdempotencyKeyField: s.IdempotencyKeyField,
			RetryPolicy:         retryPolicyEntry(s.RetryPolicy),
			Transactional:       s.Transactional,
		})
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	slices.SortFunc(out, func(a, b manifest.EventSubscription) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Version, b.Version))
	})
	return out, nil
}

func retryPolicyEntry(r *def.RetryPolicyDeclaration) *manifest.RetryPolicy {
	if r == nil {
		return nil
	}
	policy := &manifest.RetryPolicy{
		MaxAttempts:    r.MaxAttempts,
		Backoff:        r.Backoff,
		InitialDelayMS: r.InitialDelayMS,
		MaxDelayMS:     r.MaxDelayMS,
	}
	if r.NoJitter {
		policy.Jitter = new(false)
	}
	return policy
}
