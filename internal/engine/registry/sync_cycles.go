package registry

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/module"
)

// syncEdge is one directed edge in the synchronous-subscription graph: the
// owning module (map key) subscribes synchronously to event, which is
// emitted by module "to".
type syncEdge struct {
	to    string
	event string
}

// buildSyncSubscriptionGraph returns, for every non-failed module, the
// synchronous ("async": false) subscriptions it declares as edges to the
// module(s) that emit the subscribed event. Async subscriptions never
// appear here — event-system.md §8's cycle/self-subscription rule is
// scoped to synchronous dispatch only, since that's the only dispatch mode
// that can deadlock a chain of inline calls.
func buildSyncSubscriptionGraph(modules map[string]*module.LoadedModule) map[string][]syncEdge {
	emittersByEvent := make(map[string][]string)
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		for _, e := range m.Manifest.Emits {
			emittersByEvent[e.Name] = append(emittersByEvent[e.Name], name)
		}
	}

	graph := make(map[string][]syncEdge)
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		for _, sub := range m.Manifest.Subscribes {
			if sub.Async {
				continue
			}
			for _, emitter := range emittersByEvent[sub.Name] {
				graph[name] = append(graph[name], syncEdge{to: emitter, event: sub.Name})
			}
		}
	}
	return graph
}

// findSyncSubscriptionCycle returns the module names forming one
// synchronous-subscription cycle — a module subscribing synchronously to
// its own emitted event is reported as a length-1 cycle — or nil if the
// graph is acyclic. Deterministic: modules and each module's edges are
// visited in sorted order, so repeated calls against the same manifests
// report the same cycle.
func findSyncSubscriptionCycle(modules map[string]*module.LoadedModule) []string {
	graph := buildSyncSubscriptionGraph(modules)
	for _, edges := range graph {
		sort.Slice(edges, func(i, j int) bool { return edges[i].to < edges[j].to })
	}

	names := make([]string, 0, len(modules))
	for name, m := range modules {
		if m.Status != module.StatusFailed {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	const (
		white = iota
		gray
		black
	)
	color := make(map[string]int, len(names))
	var path []string

	var visit func(n string) []string
	visit = func(n string) []string {
		color[n] = gray
		path = append(path, n)
		for _, e := range graph[n] {
			switch color[e.to] {
			case white:
				if cyc := visit(e.to); cyc != nil {
					return cyc
				}
			case gray:
				idx := slices.Index(path, e.to)
				return append([]string{}, path[idx:]...)
			}
		}
		path = path[:len(path)-1]
		color[n] = black
		return nil
	}

	for _, n := range names {
		if color[n] == white {
			if cyc := visit(n); cyc != nil {
				return cyc
			}
		}
	}
	return nil
}

// validateSyncSubscriptionCycles rejects every synchronous-subscription
// cycle in modules — including a module subscribing synchronously to its
// own emitted event — by marking every module in the cycle StatusFailed.
// A synchronous dispatch chain that formed a cycle could never resolve
// (engine-internals.md §2 Stage 3 step 23, event-system.md §8), so this
// runs before any other build* pass below, letting them all naturally
// exclude the newly-failed modules the same way they already skip modules
// LoadAll failed earlier. Repeats until the graph is acyclic: failing one
// cycle can leave a second, independent cycle still to find.
func validateSyncSubscriptionCycles(modules map[string]*module.LoadedModule) {
	for {
		cycle := findSyncSubscriptionCycle(modules)
		if cycle == nil {
			return
		}

		var msg string
		if len(cycle) == 1 {
			msg = fmt.Sprintf("module %q subscribes synchronously to its own emitted event", cycle[0])
		} else {
			path := append(append([]string{}, cycle...), cycle[0])
			msg = fmt.Sprintf("synchronous subscription cycle detected: %s", strings.Join(path, " -> "))
		}

		for _, n := range cycle {
			modules[n].Fail(msg)
		}
	}
}
