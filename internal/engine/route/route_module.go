package route

import (
	"fmt"
	"strings"
)

type ExplicitRoute struct {
	Method string
	Path   string
}

func RegisterModuleRoutes(table *RouteTable, moduleName, moduleType string, routes []ExplicitRoute) error {
	scratch := New()

	type pending struct {
		method, path string
		entry        *RouteEntry
	}
	toRegister := make([]pending, 0, len(routes))

	for _, r := range routes {
		if len(r.Path) == 0 || r.Path[0] != '/' {
			return fmt.Errorf("route: module %q: declared path %q must start with \"/\"", moduleName, r.Path)
		}

		normalizedPath := normalizePath(r.Path)
		var segments []string
		if len(normalizedPath) != 0 {
			segments = strings.Split(normalizedPath, "/")
		}

		for _, segment := range segments {
			if segment == "." || segment == ".." {
				return fmt.Errorf("route: module %q: declared path %q contains a %q segment", moduleName, r.Path, segment)
			}
		}

		prefix := "/" + moduleName
		if moduleType == "connector" {
			prefix = "/connectors/" + moduleName
		}
		expandedPath := prefix
		if r.Path != "/" {
			expandedPath = prefix + r.Path
		}

		expandedSegments := strings.Split(normalizePath(expandedPath), "/")
		first := expandedSegments[0]
		switch {
		case strings.HasPrefix(first, "_"):
			return fmt.Errorf("route: module %q: %s is a reserved engine namespace", moduleName, expandedPath)
		case first == "auth" || first == "admin":
			return fmt.Errorf("route: module %q: %s is a reserved engine namespace", moduleName, expandedPath)
		case first == "connectors" && moduleType != "connector":
			return fmt.Errorf("route: module %q: %s is reserved for connector-type modules", moduleName, expandedPath)
		}

		if scratch.Registered(r.Method, expandedPath) {
			return fmt.Errorf("route: module %q: duplicate route %s %s", moduleName, r.Method, expandedPath)
		}
		entry := &RouteEntry{ModuleName: moduleName, PathTemplate: expandedPath}
		scratch.Register(r.Method, expandedPath, entry)
		toRegister = append(toRegister, pending{r.Method, expandedPath, entry})
	}

	for _, p := range toRegister {
		table.Register(p.method, p.path, p.entry)
	}
	return nil
}
