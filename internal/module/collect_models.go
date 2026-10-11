package module

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func init() {
	registerCollector(ownedModelsCollector{})
}

type ownedModelsCollector struct{}

func (ownedModelsCollector) Key() string { return "schema.owned_models" }

func (ownedModelsCollector) Kinds() []string { return []string{model.KindModel} }

func (ownedModelsCollector) Collect(d Declarations, info ModuleInfo) (any, error) {
	definitions, err := decodeDeclarations[struct {
		Name string `json:"name"`
	}](d, model.KindModel)
	if err != nil {
		return nil, err
	}

	var problems []error
	seen := make(map[string]bool, len(definitions))
	out := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		owner, resource, qualified := strings.Cut(definition.Name, ".")
		name := definition.Name
		if !qualified {
			owner, resource = info.Name, definition.Name
			name = info.Name + "." + definition.Name
		}
		if owner != info.Name || resource == "" || strings.Contains(resource, ".") {
			problems = append(problems, fmt.Errorf("model %q must use the module's own %s.{resource} name", definition.Name, info.Name))
		}
		if seen[name] {
			problems = append(problems, fmt.Errorf("model %q is declared more than once", name))
		}
		seen[name] = true
		out = append(out, name)
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	slices.Sort(out)

	return out, nil
}
