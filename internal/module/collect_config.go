package module

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/config/def"
)

func init() {
	registerCollector(configSchemaCollector{})
	registerCollector(usesConfigCollector{})
}

var configShortKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

type configSchemaCollector struct{}

func (configSchemaCollector) Key() string { return "config_schema" }

func (configSchemaCollector) Kinds() []string { return []string{def.KindConfig} }

func (configSchemaCollector) Collect(d Declarations, _ ModuleInfo) (any, error) {
	definitions, err := decodeDeclarations[def.Declaration](d, def.KindConfig)
	if err != nil {
		return nil, err
	}

	var problems []error
	seen := make(map[string]bool, len(definitions))
	out := make([]manifest.ConfigEntry, 0, len(definitions))
	for _, definition := range definitions {
		if !configShortKeyPattern.MatchString(definition.Key) {
			problems = append(problems, fmt.Errorf("config key %q must be non-empty alphanumerics and underscores, with no module prefix", definition.Key))
		}
		if seen[definition.Key] {
			problems = append(problems, fmt.Errorf("config key %q is declared more than once", definition.Key))
		}
		seen[definition.Key] = true
		if definition.Required && definition.Default != nil {
			problems = append(problems, fmt.Errorf("config key %q is Required, so its manifest default must be null", definition.Key))
		}

		entry := manifest.ConfigEntry{
			Key:             definition.Key,
			Type:            definition.Type,
			Default:         definition.Default,
			Label:           definition.Label,
			Description:     definition.Description,
			FieldType:       definition.FieldType,
			Required:        definition.Required,
			Category:        definition.Category,
			Public:          definition.Public,
			RestartRequired: definition.RestartRequired,
			ValidationRegex: definition.Pattern,
			Encrypted:       definition.Encrypted,
			Generated:       definition.Generated,
			Min:             definition.Min,
			Max:             definition.Max,
		}
		for _, choice := range definition.Choices {
			entry.Options = append(entry.Options, manifest.FieldOption{Value: choice.Value, Label: choice.Label})
		}
		out = append(out, entry)
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	slices.SortFunc(out, func(a, b manifest.ConfigEntry) int { return cmp.Compare(a.Key, b.Key) })

	return out, nil
}

type usesConfigCollector struct{}

func (usesConfigCollector) Key() string { return "uses_config" }

func (usesConfigCollector) Kinds() []string { return []string{def.KindRef} }

func (usesConfigCollector) Collect(d Declarations, info ModuleInfo) (any, error) {
	refs, err := decodeDeclarations[def.RefDeclaration](d, def.KindRef)
	if err != nil {
		return nil, err
	}

	var problems []error
	seen := make(map[string]bool, len(refs))
	out := make([]manifest.UsesConfigRef, 0, len(refs))
	for _, ref := range refs {
		owner, key, ok := strings.Cut(ref.Key, ".")
		if !ok || !configShortKeyPattern.MatchString(owner) || !configShortKeyPattern.MatchString(key) || owner == "company" {
			problems = append(problems, fmt.Errorf("config.Ref %q must be another module's {module}.{key} name; use the company handles for platform keys", ref.Key))
		}
		if owner == info.Name {
			problems = append(problems, fmt.Errorf("config.Ref %q names the module's own key; declare it with a config constructor", ref.Key))
		}
		if seen[ref.Key] {
			problems = append(problems, fmt.Errorf("config.Ref key %q is declared more than once", ref.Key))
		}
		seen[ref.Key] = true
		out = append(out, manifest.UsesConfigRef{Key: ref.Key, Type: ref.Type})
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	slices.SortFunc(out, func(a, b manifest.UsesConfigRef) int { return cmp.Compare(a.Key, b.Key) })

	return out, nil
}
