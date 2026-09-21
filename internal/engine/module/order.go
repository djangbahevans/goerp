package module

import "sort"

// OrderByDependencies returns mods ordered so that each module follows the
// modules it lists in depends_on. Modules are visited in name order, so the
// result is deterministic. A depends_on naming a module absent from mods is
// ignored, and a cycle, which boot rejects, is broken where it closes.
func OrderByDependencies(mods []*LoadedModule) []*LoadedModule {
	byName := make(map[string]*LoadedModule, len(mods))
	names := make([]string, 0, len(mods))
	for _, m := range mods {
		byName[m.Manifest.Name] = m
		names = append(names, m.Manifest.Name)
	}
	sort.Strings(names)

	ordered := make([]*LoadedModule, 0, len(mods))
	visited := make(map[string]bool, len(mods))
	var visit func(name string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		for _, dep := range byName[name].Manifest.DependsOn {
			if _, present := byName[dep]; present {
				visit(dep)
			}
		}
		ordered = append(ordered, byName[name])
	}
	for _, name := range names {
		visit(name)
	}
	return ordered
}
