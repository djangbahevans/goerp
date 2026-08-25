// Package computed indexes .Computed()/.Depends() field declarations
// across every loaded module into a reverse lookup: given a model and the
// fields a write just touched, which computed fields elsewhere need to
// recompute (go-sdk-reference.md §22 "Computed field recomputation").
//
// This package imports only sdk/go/model — deliberately, so
// internal/engine/wasm (which cannot import internal/engine/registry or
// internal/engine/module without an import cycle, module.LoadedModule
// itself holding a *wasm.InstancePool) can still consume the built index,
// the same reasoning internal/engine/fieldsec already established for
// FieldSecurityRegistry.
package computed

import (
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// Dependent is one computed field whose value depends on a field this
// index was queried about. ModelDecl is the full declaration of the
// model the computed field lives on, so a caller resolving a Many2One-hop
// dependent never needs a second, cross-module model lookup (host.orm's
// own resolveModel deliberately only resolves a caller's own models,
// host_orm.go).
type Dependent struct {
	ModuleName string
	ModelDecl  model.ModelDeclaration
	Field      string // the computed field's name
	ComputeFn  string

	// ViaFKField is "" for a same-record dependency. Otherwise it names
	// the Many2One field (e.g. "customer_id") on ModelDecl whose related
	// record is the one that changed — Lookup is called with the
	// *related* model's qualified name and the field that changed there;
	// the caller resolves ViaFKField's value on each of ModelDecl's own
	// rows to find which specific records need recomputing.
	ViaFKField string

	// ViaChildFKField is "" unless this dependency was declared through a
	// One2Many field on ModelDecl (go-sdk-reference.md §22 "One2Many" /
	// "Computed field recomputation"). It names the Many2One field on the
	// *child* model (the One2Many field's own InverseField) that points
	// back at ModelDecl — the mirror image of ViaFKField: instead of a
	// WHERE query against many candidate rows, the caller reads
	// ViaChildFKField's value directly off the child row that was just
	// created/written/unlinked, since a child names its one parent
	// directly.
	ViaChildFKField string
}

// dependentKey identifies a computed field uniquely enough to de-dupe
// Lookup's merge across multiple changed fields (a field depending on two
// changed fields at once must only be recomputed once).
type dependentKey struct {
	model string
	field string
}

// Index is the reverse-dependency lookup — built once per
// registry.ModuleRegistry.Update() (mirroring
// internal/engine/fieldsec.FieldSecurityRegistry's own build-once-per-
// snapshot shape) and never mutated after Register calls finish.
type Index struct {
	// sameRecord: "module.model" -> changed field name -> dependents on
	// that same model.
	sameRecord map[string]map[string][]Dependent

	// viaHop: "module.model" (the RELATED model a Many2One field points
	// at) -> changed field name on that related model -> dependents
	// elsewhere whose Many2One field points here.
	viaHop map[string]map[string][]Dependent

	// viaChild: "module.model" (the CHILD model a One2Many field names)
	// -> changed field name on that child model -> dependents on the
	// parent that declared the One2Many field.
	viaChild map[string]map[string][]Dependent
}

func New() *Index {
	return &Index{
		sameRecord: make(map[string]map[string][]Dependent),
		viaHop:     make(map[string]map[string][]Dependent),
		viaChild:   make(map[string]map[string][]Dependent),
	}
}

// Register indexes every Computed field declared across decls (all owned
// by moduleName) against the DependsOn paths it names. A DependsOn entry
// that doesn't resolve — no dot and the named field doesn't exist, a
// dotted path whose relField+"_id" isn't a declared Many2One field on the
// same model, and relField itself isn't a declared One2Many field on the
// same model either — is silently skipped rather than erroring: this
// package has no load-time validation authority (that belongs to
// internal/engine/loader, a separate concern), and a malformed
// declaration should recompute nothing rather than panic.
func (idx *Index) Register(moduleName string, decls []model.ModelDeclaration) {
	for _, decl := range decls {
		qualifiedModel := moduleName + "." + decl.Name

		for _, field := range decl.Fields {
			if !field.Def.IsComputed {
				continue
			}
			dep := Dependent{
				ModuleName: moduleName,
				ModelDecl:  decl,
				Field:      field.Name,
				ComputeFn:  field.Def.ComputeFn,
			}

			for _, path := range field.Def.DependsOn {
				relField, remoteField, hop := strings.Cut(path, ".")
				if !hop {
					idx.addSameRecord(qualifiedModel, relField, dep)
					continue
				}

				if fkField, ok := many2OneField(decl, relField+"_id"); ok {
					idx.addViaHop(fkField.Def.RelatedModel, remoteField, withViaFK(dep, fkField.Name))
					continue
				}
				if o2mField, ok := one2ManyField(decl, relField); ok {
					idx.addViaChild(o2mField.Def.RelatedModel, remoteField, withViaChildFK(dep, o2mField.Def.InverseField))
				}
			}
		}
	}
}

func (idx *Index) addSameRecord(model, field string, dep Dependent) {
	if idx.sameRecord[model] == nil {
		idx.sameRecord[model] = make(map[string][]Dependent)
	}
	idx.sameRecord[model][field] = append(idx.sameRecord[model][field], dep)
}

func (idx *Index) addViaHop(relatedModel, field string, dep Dependent) {
	if idx.viaHop[relatedModel] == nil {
		idx.viaHop[relatedModel] = make(map[string][]Dependent)
	}
	idx.viaHop[relatedModel][field] = append(idx.viaHop[relatedModel][field], dep)
}

func (idx *Index) addViaChild(childModel, field string, dep Dependent) {
	if idx.viaChild[childModel] == nil {
		idx.viaChild[childModel] = make(map[string][]Dependent)
	}
	idx.viaChild[childModel][field] = append(idx.viaChild[childModel][field], dep)
}

func withViaFK(dep Dependent, fkField string) Dependent {
	dep.ViaFKField = fkField
	return dep
}

func withViaChildFK(dep Dependent, inverseField string) Dependent {
	dep.ViaChildFKField = inverseField
	return dep
}

// many2OneField finds a declared Many2One field named fieldName on decl.
func many2OneField(decl model.ModelDeclaration, fieldName string) (model.NamedField, bool) {
	for _, f := range decl.Fields {
		if f.Name == fieldName && f.Def.Kind == model.KindMany2One {
			return f, true
		}
	}
	return model.NamedField{}, false
}

// one2ManyField finds a declared One2Many field named fieldName on decl.
func one2ManyField(decl model.ModelDeclaration, fieldName string) (model.NamedField, bool) {
	for _, f := range decl.Fields {
		if f.Name == fieldName && f.Def.Kind == model.KindOne2Many {
			return f, true
		}
	}
	return model.NamedField{}, false
}

// Lookup returns every computed field that depends on any field in
// changedFields on qualifiedModel — same-record dependents, dependents
// reached through a Many2One hop pointing at qualifiedModel, and
// dependents reached through a One2Many hop naming qualifiedModel as the
// child — de-duplicated so a field depending on more than one changed
// field appears once. Callers where the written row no longer exists
// (host.orm's unlink path) must use LookupViaChild instead: same-record
// and Many2One-hop dependents both assume qualifiedModel's own row is
// still there to read or update.
func (idx *Index) Lookup(qualifiedModel string, changedFields []string) []Dependent {
	seen := make(map[dependentKey]bool)
	var out []Dependent

	add := func(dep Dependent) {
		key := dependentKey{model: dep.ModuleName + "." + dep.ModelDecl.Name, field: dep.Field}
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, dep)
	}

	for _, field := range changedFields {
		for _, dep := range idx.sameRecord[qualifiedModel][field] {
			add(dep)
		}
		for _, dep := range idx.viaHop[qualifiedModel][field] {
			add(dep)
		}
		for _, dep := range idx.viaChild[qualifiedModel][field] {
			add(dep)
		}
	}

	return out
}

// LookupViaChild returns only the One2Many-hop dependents reached from a
// child row's changedFields on qualifiedChildModel — the delete-safe
// subset of Lookup's results, for host.orm's unlink path (the child row
// being processed no longer exists by the time recompute runs, so
// same-record and Many2One-hop dependents, which assume it does, are
// deliberately excluded here).
func (idx *Index) LookupViaChild(qualifiedChildModel string, changedFields []string) []Dependent {
	seen := make(map[dependentKey]bool)
	var out []Dependent

	for _, field := range changedFields {
		for _, dep := range idx.viaChild[qualifiedChildModel][field] {
			key := dependentKey{model: dep.ModuleName + "." + dep.ModelDecl.Name, field: dep.Field}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, dep)
		}
	}

	return out
}
