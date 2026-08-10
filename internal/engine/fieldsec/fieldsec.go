package fieldsec

import (
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

func New() *FieldSecurityRegistry {
	return &FieldSecurityRegistry{rules: make(map[string]map[string]FieldSecurityRule)}
}

func (r *FieldSecurityRegistry) Register(moduleName string, modelDecls []model.ModelDeclaration) {
	for _, decl := range modelDecls {
		modelName := moduleName + "." + decl.Name
		for _, field := range decl.Fields {
			rule, ok := fieldSecurityRuleFor(field)
			if !ok {
				continue
			}
			if r.rules[modelName] == nil {
				r.rules[modelName] = make(map[string]FieldSecurityRule)
			}
			r.rules[modelName][field.Name] = rule
		}
	}
}

func fieldSecurityRuleFor(field model.NamedField) (FieldSecurityRule, bool) {
	// FieldDef carries no security data until SDK backlog #19 lands; always empty for now
	return FieldSecurityRule{}, false
}
