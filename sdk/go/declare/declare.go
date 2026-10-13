// Package declare is the registry the module-side declaration kinds record
// into at init() — event, subscription, job, cron and the other definitions
// whose manifest blocks `goerp module generate` produces. The generator builds the module's cmd/module
// package, runs it in a sandbox and reads the registry back through Export,
// so a declaration kind needs no code in the generator's collection step: it
// calls Add and a collector turns its declarations into a manifest block.
package declare

import (
	"encoding/json/v2"
	"io"
)

type entry struct {
	kind        string
	declaration any
}

var entries []entry
var checks []func() error

// Add records one declaration of kind, called from init() or a package-level
// var initialiser. declaration is encoded with encoding/json/v2 when the
// registry is exported, so it carries json tags for the wire keys its
// collector reads.
func Add(kind string, declaration any) {
	entries = append(entries, entry{kind: kind, declaration: declaration})
}

// ValidateWith registers a check that runs before declarations are exported.
// A failed check returns its error and prevents any declaration output.
func ValidateWith(check func() error) {
	checks = append(checks, check)
}

// Export validates and encodes recorded declarations as a JSON object mapping
// each kind to its declarations in registration order. Validation and encoding
// errors abort the export.
func Export() ([]byte, error) {
	for _, check := range checks {
		if err := check(); err != nil {
			return nil, err
		}
	}
	byKind := make(map[string][]any)
	for _, e := range entries {
		byKind[e.kind] = append(byKind[e.kind], e.declaration)
	}
	return json.Marshal(byKind, json.Deterministic(true))
}

// WriteTo writes Export's output to w.
func WriteTo(w io.Writer) error {
	data, err := Export()
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
