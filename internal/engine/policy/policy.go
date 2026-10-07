// Package policy indexes the ABAC policies of the loaded modules by the
// permission each one scopes, resolved to the table and columns needed to
// evaluate it against one record (auth-internals.md §11 "Policy enforcement
// points", §13 "Permission evaluation pipeline").
package policy

import (
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// Policy is one manifest policy ready to evaluate against a record of its
// target table.
type Policy struct {
	Name string
	Expr domain.Expr

	// Table and PKColumn locate the record; Columns are the fields Expr
	// reads.
	Table    string
	PKColumn string
	Columns  []string

	// Unresolved is why the policy cannot be evaluated: its condition does
	// not parse or the model it governs is not loaded. It is kept rather
	// than dropped, so a check that depends on it fails instead of falling
	// back to a role-only answer.
	Unresolved error
}

// Registry maps a permission name to the policies that scope it.
type Registry struct {
	byPermission map[string][]Policy
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{byPermission: make(map[string][]Policy)}
}

// Register adds a module's policies. The model a policy governs is the one
// named by its applies_to permission, which can belong to another module, so
// modelsByModule maps every loaded module to its declared models. A policy
// that cannot be resolved is registered with Unresolved set.
func (r *Registry) Register(policies []manifest.Policy, modelsByModule map[string][]model.ModelDeclaration) {
	for _, p := range policies {
		r.byPermission[p.AppliesTo] = append(r.byPermission[p.AppliesTo], resolve(p, modelsByModule))
	}
}

func resolve(p manifest.Policy, modelsByModule map[string][]model.ModelDeclaration) Policy {
	unresolved := func(format string, args ...any) Policy {
		return Policy{Name: p.Name, Unresolved: fmt.Errorf(format, args...)}
	}

	owner, resource, _ := splitPermission(p.AppliesTo)
	md, ok := findModel(modelsByModule[owner], owner, resource)
	if !ok {
		return unresolved("applies_to %q: no loaded model %s.%s", p.AppliesTo, owner, resource)
	}
	pk, ok := primaryKey(md)
	if !ok {
		return unresolved("model %s.%s has no primary key", owner, resource)
	}
	expr, err := domain.Parse(p.Condition)
	if err != nil {
		return unresolved("condition failed to parse: %w", err)
	}
	return Policy{
		Name:     p.Name,
		Expr:     expr,
		Table:    modeltable.Name(md),
		PKColumn: pk,
		Columns:  domain.RecordFields(expr),
	}
}

// For returns the policies that scope permission, nil when there are none.
func (r *Registry) For(permission string) []Policy {
	if r == nil {
		return nil
	}
	return r.byPermission[permission]
}

func splitPermission(name string) (module, resource, action string) {
	parts := strings.Split(name, ":")
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

func findModel(decls []model.ModelDeclaration, module, resource string) (model.ModelDeclaration, bool) {
	qualified := module + "." + resource
	for _, d := range decls {
		if d.QualifiedName(module) == qualified {
			return d, true
		}
	}
	return model.ModelDeclaration{}, false
}

func primaryKey(md model.ModelDeclaration) (string, bool) {
	for _, f := range md.Fields {
		if f.Def.IsPrimaryKey {
			return f.Name, true
		}
	}
	return "", false
}
