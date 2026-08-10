package fieldsec

import (
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type ReadBehaviour int

const (
	Omit ReadBehaviour = iota
	Nullify
	Mask
)

type WriteBehaviour int

const (
	Reject WriteBehaviour = iota
	Ignore
)

type FieldSecurityRule struct {
	ReadPermission  string // "" = no restriction
	WritePermission string // "" = no restriction
	OnDeniedRead    ReadBehaviour
	OnDeniedWrite   WriteBehaviour
	MaskPattern     string
}

type FieldSecurityRegistry struct {
	rules map[string]map[string]FieldSecurityRule // model_name -> field_name -> rule
}

func (r *FieldSecurityRegistry) Rule(modelName, fieldName string) (FieldSecurityRule, bool) {
	if rules, ok := r.rules[modelName]; ok {
		if rule, ok := rules[fieldName]; ok {
			return rule, ok
		}
	}

	return FieldSecurityRule{}, false
}

func buildFieldSecRegistry(modules map[string]*module.LoadedModule) *FieldSecurityRegistry {
	reg := &FieldSecurityRegistry{rules: make(map[string]map[string]FieldSecurityRule)}
	for moduleName, m := range modules {
		for _, decl := range m.ModelDecls {
			modelName := moduleName + "." + decl.Name
			for _, field := range decl.Fields {
				rule, ok := fieldSecurityRuleFor(field)
				if !ok {
					continue
				}
				if reg.rules[modelName] == nil {
					reg.rules[modelName] = make(map[string]FieldSecurityRule)
				}
				reg.rules[modelName][field.Name] = rule
			}
		}
	}

	return reg
}

func fieldSecurityRuleFor(field model.NamedField) (FieldSecurityRule, bool) {
	// FieldDef carries no security data until SDK backlog #19 lands; always empty for now
	return FieldSecurityRule{}, false
}
