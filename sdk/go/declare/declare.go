// Package declare is the registry the module-side declaration kinds record
// into at init() — event, subscription, job, cron and the other definitions
// whose manifest blocks `goerp module generate` produces (go-sdk-reference.md
// §7 "Manifest generation"). The generator builds the module's cmd/module
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

// Add records one declaration of kind, called from init() or a package-level
// var initialiser. declaration is encoded with encoding/json/v2 when the
// registry is exported, so it carries json tags for the wire keys its
// collector reads.
func Add(kind string, declaration any) {
	entries = append(entries, entry{kind: kind, declaration: declaration})
}

// Export encodes every recorded declaration as a JSON object mapping each
// kind to its declarations in registration order.
func Export() ([]byte, error) {
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
