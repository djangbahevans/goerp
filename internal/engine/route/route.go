package route

import (
	"maps"
	"net/url"
	"slices"
	"strings"
)

type RouteTable struct {
	tree *node
}

type node struct {
	staticChildren map[string]*node
	paramChild     *node
	paramName      string
	handlers       map[string]*RouteEntry
}

type RouteEntry struct {
	ModuleName   string
	Manifest     RouteManifest
	PathTemplate string
}

type LookupResult int

const (
	RouteFound LookupResult = iota
	RouteNotFound
	RouteMethodNotAllowed
	RouteBadPath
)

func New() *RouteTable {
	return &RouteTable{
		tree: &node{
			staticChildren: make(map[string]*node),
			handlers:       make(map[string]*RouteEntry),
		},
	}
}

func (rt *RouteTable) Register(method, path string, entry *RouteEntry) {
	normalizedPath := normalizePath(path)

	var segments []string
	if len(normalizedPath) != 0 {
		segments = strings.Split(normalizedPath, "/")
	}

	current := rt.tree

	for _, segment := range segments {
		if segment[0] == '{' && segment[len(segment)-1] == '}' {
			if current.paramChild == nil {
				current.paramChild = &node{}
				current.paramChild.staticChildren = make(map[string]*node)
				current.paramChild.handlers = make(map[string]*RouteEntry)
				current.paramChild.paramName = segment[1 : len(segment)-1]
			}
			current = current.paramChild
		} else {
			if _, ok := current.staticChildren[segment]; !ok {
				current.staticChildren[segment] = &node{}
				current.staticChildren[segment].staticChildren = make(map[string]*node)
				current.staticChildren[segment].handlers = make(map[string]*RouteEntry)
			}
			current = current.staticChildren[segment]
		}
	}

	current.handlers[method] = entry
}

func (rt *RouteTable) Lookup(method, path string) (*RouteEntry, map[string]string, LookupResult, []string) {
	normalizedPath := normalizePath(path)

	var segments []string
	if len(normalizedPath) != 0 {
		segments = strings.Split(normalizedPath, "/")
	}

	for i, segment := range segments {
		if segment == "." || segment == ".." {
			return nil, nil, RouteBadPath, nil
		}
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return nil, nil, RouteBadPath, nil
		}
		segments[i] = decoded
	}

	current := rt.tree
	pathParams := make(map[string]string)

	for _, segment := range segments {
		if child, ok := current.staticChildren[segment]; ok {
			current = child
		} else if current.paramChild != nil {
			current = current.paramChild
			pathParams[current.paramName] = segment
		} else {
			return nil, nil, RouteNotFound, nil
		}
	}

	if handler, ok := current.handlers[method]; ok {
		return handler, pathParams, RouteFound, nil
	}

	if len(current.handlers) != 0 {
		allowedMethods := slices.Collect(maps.Keys(current.handlers))
		slices.Sort(allowedMethods)
		return nil, nil, RouteMethodNotAllowed, allowedMethods
	}

	return nil, nil, RouteNotFound, nil
}

func (rt *RouteTable) find(segments []string) *node {
	current := rt.tree

	for _, segment := range segments {
		if segment == "" {
			continue
		}

		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			if current.paramChild == nil {
				return nil
			}
			current = current.paramChild
			continue
		}

		child, ok := current.staticChildren[segment]
		if !ok {
			return nil
		}
		current = child
	}

	return current
}

// AllRoute is one (method, entry) pair returned by RouteTable.All.
type AllRoute struct {
	Method string
	Entry  *RouteEntry
}

// All returns every route registered in rt — module-declared and engine
// built-in alike — sorted by (path, method) for a deterministic order
// (mirroring Lookup's own sorted-methods convention for
// RouteMethodNotAllowed). Entry.PathTemplate already carries each route's
// full expanded path, so callers never need to reconstruct one from the
// trie's own segment structure.
func (rt *RouteTable) All() []AllRoute {
	var out []AllRoute
	rt.tree.collectAll(&out)
	slices.SortFunc(out, func(a, b AllRoute) int {
		if c := strings.Compare(a.Entry.PathTemplate, b.Entry.PathTemplate); c != 0 {
			return c
		}
		return strings.Compare(a.Method, b.Method)
	})
	return out
}

func (n *node) collectAll(out *[]AllRoute) {
	for _, method := range slices.Sorted(maps.Keys(n.handlers)) {
		*out = append(*out, AllRoute{Method: method, Entry: n.handlers[method]})
	}
	for _, seg := range slices.Sorted(maps.Keys(n.staticChildren)) {
		n.staticChildren[seg].collectAll(out)
	}
	if n.paramChild != nil {
		n.paramChild.collectAll(out)
	}
}

func (rt *RouteTable) Registered(method, path string) bool {
	normalizedPath := normalizePath(path)

	var segments []string
	if normalizedPath != "" {
		segments = strings.Split(normalizedPath, "/")
	}

	n := rt.find(segments)
	if n == nil {
		return false
	}

	_, registered := n.handlers[method]
	return registered
}

func normalizePath(path string) string {
	var b strings.Builder
	previousSlash := false

	for _, c := range path {
		if c == '/' {
			if previousSlash {
				continue
			}
			previousSlash = true
		} else {
			previousSlash = false
		}

		b.WriteRune(c)
	}

	normalized := b.String()
	normalized = strings.Trim(normalized, "/")
	return normalized
}
