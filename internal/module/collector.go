package module

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
)

// Declarations is what a module's init() functions recorded into
// sdk/go/declare, keyed by declaration kind, each kind's entries in
// registration order and still JSON-encoded.
type Declarations map[string][]jsontext.Value

// decodeDeclarations decodes every declaration of kind into a T.
func decodeDeclarations[T any](d Declarations, kind string) ([]T, error) {
	out := make([]T, 0, len(d[kind]))
	for _, raw := range d[kind] {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("decode %s declaration %s: %w", kind, raw, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// ModuleInfo is what a collector knows of the module besides its declarations.
type ModuleInfo struct {
	// Name is the manifest's module name.
	Name string
}

// Collector turns the declarations of one or more kinds into one generated
// top-level manifest key. Adding a declaration kind means adding a collector
// with registerCollector; the collection step that builds and runs the module
// does not change.
type Collector interface {
	// Key is the manifest key whose value the collector generates.
	Key() string
	// Kinds lists the declaration kinds the collector reads. A declared
	// kind that no collector lists fails generation, so a mistyped kind
	// cannot silently drop its declarations.
	Kinds() []string
	// Collect validates the cross-references the declarations make and
	// returns the key's value. A nil value, or one that encodes to null,
	// an empty array or an empty object, leaves the key out of the
	// manifest.
	Collect(Declarations, ModuleInfo) (any, error)
}

var collectors []Collector

// registerCollector adds c to the collectors Generate runs, called from
// init(). It panics when another collector already generates c's key.
func registerCollector(c Collector) {
	for _, existing := range collectors {
		if existing.Key() == c.Key() {
			panic(fmt.Sprintf("module: manifest key %q already has a collector", c.Key()))
		}
	}
	collectors = append(collectors, c)
}

// collectBlocks runs every collector over decls and returns the blocks in key
// order, so the manifest is rewritten identically however the collectors were
// registered. The first collector error stops generation.
func collectBlocks(decls Declarations, info ModuleInfo, cs []Collector) ([]generatedBlock, error) {
	if err := checkKindsClaimed(decls, cs); err != nil {
		return nil, err
	}

	blocks := make([]generatedBlock, 0, len(cs))
	for _, c := range cs {
		v, err := c.Collect(decls, info)
		if err != nil {
			return nil, fmt.Errorf("collect %s: %w", c.Key(), err)
		}

		block := generatedBlock{key: c.Key()}
		if v != nil {
			encoded, err := json.Marshal(v, json.Deterministic(true))
			if err != nil {
				return nil, fmt.Errorf("encode %s: %w", c.Key(), err)
			}
			if !isEmptyJSON(encoded) {
				block.value = encoded
			}
		}
		blocks = append(blocks, block)
	}

	slices.SortFunc(blocks, func(a, b generatedBlock) int { return cmp.Compare(a.key, b.key) })
	return blocks, nil
}

func isEmptyJSON(v jsontext.Value) bool {
	switch string(v) {
	case "null", "[]", "{}":
		return true
	}
	return false
}

// checkKindsClaimed fails when decls holds a kind none of cs reads.
func checkKindsClaimed(decls Declarations, cs []Collector) error {
	var unclaimed []string
	for kind := range decls {
		if !slices.ContainsFunc(cs, func(c Collector) bool { return slices.Contains(c.Kinds(), kind) }) {
			unclaimed = append(unclaimed, kind)
		}
	}
	if len(unclaimed) == 0 {
		return nil
	}
	slices.Sort(unclaimed)
	return fmt.Errorf("declarations of kind %s have no collector", strings.Join(unclaimed, ", "))
}
