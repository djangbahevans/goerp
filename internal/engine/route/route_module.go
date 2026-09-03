package route

import (
	"fmt"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/engine"
)

type ExplicitRoute struct {
	Method string
	Path   string

	Auth         string
	Permissions  []string
	RateLimit    *RateLimitConfig
	MaxBodyBytes int64
	RawBody      bool
	Timeout      time.Duration
	Streaming    bool
	Websocket    bool
	PathParams   map[string]string
}

// ExplicitRoutesFrom converts a module's deserialized get_routes output
// (sdk/go/engine.RouteDeclaration, the wire shape) into the engine's own
// ExplicitRoute — the one place this field-by-field mapping happens, so
// every LoadAll/LoadCascading/registry.buildRouteTable call site stays in
// sync instead of repeating the same conversion three times.
func ExplicitRoutesFrom(decls []engine.RouteDeclaration) []ExplicitRoute {
	out := make([]ExplicitRoute, len(decls))
	for i, d := range decls {
		var rl *RateLimitConfig
		if d.RateLimit != nil {
			rl = &RateLimitConfig{
				Requests:      d.RateLimit.Requests,
				WindowSeconds: d.RateLimit.WindowSeconds,
				Scope:         string(d.RateLimit.Scope),
			}
		}
		out[i] = ExplicitRoute{
			Method:       d.Method,
			Path:         d.Path,
			Auth:         d.Auth,
			Permissions:  d.Permissions,
			RateLimit:    rl,
			MaxBodyBytes: int64(d.MaxBodyBytes),
			RawBody:      d.RawBody,
			Timeout:      time.Duration(d.TimeoutMs) * time.Millisecond,
			Streaming:    d.Streaming,
			Websocket:    d.Websocket,
			PathParams:   d.PathParams,
		}
	}
	return out
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

		prefix := ModulePathPrefix(moduleName, moduleType)
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
		// table (unlike scratch) carries routes already committed by
		// earlier registrations against this same table.
		if table.Registered(r.Method, expandedPath) {
			return fmt.Errorf("route: module %q: %s %s already registered", moduleName, r.Method, expandedPath)
		}
		entry := &RouteEntry{
			ModuleName:   moduleName,
			PathTemplate: expandedPath,
			Manifest: RouteManifest{
				Auth:         r.Auth,
				Permissions:  r.Permissions,
				RateLimit:    r.RateLimit,
				MaxBodyBytes: r.MaxBodyBytes,
				RawBody:      r.RawBody,
				Timeout:      r.Timeout,
				Streaming:    r.Streaming,
				Websocket:    r.Websocket,
				PathParams:   r.PathParams,
			},
		}
		scratch.Register(r.Method, expandedPath, entry)
		toRegister = append(toRegister, pending{r.Method, expandedPath, entry})
	}

	for _, p := range toRegister {
		table.Register(p.method, p.path, p.entry)
	}
	return nil
}
