package route

import (
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// SuppressedView names an EnableViews-derived (model, view) candidate
// that was dropped because a hand-declared view already claimed the same
// name, or the same (resource, type) pair — the view-synthesis
// counterpart to SuppressedRoute.
type SuppressedView struct {
	Model string
	View  string
}

// SynthesizeViews derives manifest.View values from each model's
// EnableViews declaration and merges each .Nav() declaration into
// navigation — the view/nav counterpart to RegisterModelRoutes
// (go-sdk-reference.md §22 "EnableViews", "Nav"). A hand-declared view in
// existingViews with the same Name or (Resource, Type) pair wins outright
// over a synthesized one, which is dropped and reported in the returned
// suppressed slice rather than merged. EnableViews(ListView) requires
// List in EnableOps; EnableViews(FormView) requires Get+Create+Update;
// .Nav() requires EnableViews(ListView) — violating any of these aborts
// the whole call with an error. View/NavItem Permission fields are left
// empty: EnableOps-derived routes don't yet derive a permission from the
// model's Access() rules for this to read back.
func SynthesizeViews(moduleName, moduleType string, models []model.ModelDeclaration, existingViews []manifest.View, navigation []manifest.NavGroup) ([]manifest.View, []SuppressedView, []manifest.NavGroup, error) {
	existingByName := make(map[string]bool, len(existingViews))
	existingByResourceType := make(map[string]string, len(existingViews))
	for _, v := range existingViews {
		existingByName[v.Name] = true
		existingByResourceType[v.Resource+"\x00"+v.Type] = v.Name
	}

	// Cloned (including each group's Children) so mergeNavItem's in-place
	// appends below never alias or corrupt the caller's own slice — skipped
	// entirely when no model declares .Nav(), the common case, since
	// mergeNavItem is then never reached.
	mergedNav := navigation
	for _, md := range models {
		if md.NavDecl != nil {
			mergedNav = cloneNav(navigation)
			break
		}
	}

	var synthesized []manifest.View
	var suppressed []SuppressedView
	claimedThisCall := make(map[string]string, len(models)) // view name -> qualified model that claimed it

	for _, md := range models {
		if len(md.EnabledViews) == 0 && md.NavDecl == nil {
			continue
		}

		qualifiedModel := moduleName + "." + md.Name
		hasOp := func(op model.Op) bool {
			for _, o := range md.EnabledOps {
				if o.Name == op.Name {
					return true
				}
			}
			return false
		}

		hasListView := false
		hasFormView := false
		for _, vt := range md.EnabledViews {
			switch vt.Name {
			case model.ListView.Name:
				hasListView = true
			case model.FormView.Name:
				hasFormView = true
			}
		}

		if hasListView && !hasOp(model.List) {
			return nil, nil, nil, fmt.Errorf("route: module %q: model %q declares EnableViews(ListView) without List in EnableOps", moduleName, qualifiedModel)
		}
		if hasFormView && (!hasOp(model.Get) || !hasOp(model.Create) || !hasOp(model.Update)) {
			return nil, nil, nil, fmt.Errorf("route: module %q: model %q declares EnableViews(FormView) without Get, Create, and Update all in EnableOps", moduleName, qualifiedModel)
		}
		if md.NavDecl != nil && !hasListView {
			return nil, nil, nil, fmt.Errorf("route: module %q: model %q declares .Nav() without EnableViews(ListView)", moduleName, qualifiedModel)
		}

		baseName := strings.ReplaceAll(qualifiedModel, ".", "_")
		listName := baseName + "_list"
		formName := baseName + "_form"

		// Two distinct models deriving the same view name (the underscore
		// substitution isn't injective — "foo.bar" and "foo_bar" both land
		// on "foo_bar") have no legitimate winner the way a hand-declared
		// view legitimately overrides a synthesized one, so this is a
		// load-time error instead — the same category RegisterModelRoutes
		// applies to two models deriving the same route.
		if hasListView {
			if claimant, ok := claimedThisCall[listName]; ok {
				return nil, nil, nil, fmt.Errorf("route: module %q: models %q and %q both derive view %q from EnableViews", moduleName, claimant, qualifiedModel, listName)
			}
			claimedThisCall[listName] = qualifiedModel
		}
		if hasFormView {
			if claimant, ok := claimedThisCall[formName]; ok {
				return nil, nil, nil, fmt.Errorf("route: module %q: models %q and %q both derive view %q from EnableViews", moduleName, claimant, qualifiedModel, formName)
			}
			claimedThisCall[formName] = qualifiedModel
		}

		// Resolved up front, before building either view, so a suppressed
		// view's dependents (the list's "New X" action, the nav item) still
		// reference whichever view actually ends up live — the winning
		// hand-declared view's own name, not the dropped synthesized one.
		var effectiveListName, effectiveFormName string
		var listDropped, formDropped bool
		if hasListView {
			effectiveListName, listDropped = resolveViewName(listName, qualifiedModel, "list", existingByName, existingByResourceType)
			if listDropped {
				suppressed = append(suppressed, SuppressedView{Model: qualifiedModel, View: listName})
			}
		}
		if hasFormView {
			effectiveFormName, formDropped = resolveViewName(formName, qualifiedModel, "form", existingByName, existingByResourceType)
			if formDropped {
				suppressed = append(suppressed, SuppressedView{Model: qualifiedModel, View: formName})
			}
		}

		if hasListView && !listDropped {
			v := manifest.View{
				Name:     listName,
				Type:     "list",
				Resource: qualifiedModel,
				Label:    displayLabel(md, md.LabelPlural),
				Columns:  synthesizeColumns(md),
			}
			// hasFormView already implies Create is enabled (validated above),
			// so this only needs to check that there's a form to link to.
			if hasFormView {
				v.Actions = []manifest.Action{
					{Label: "New " + displayLabel(md, md.Label), Type: "create", View: effectiveFormName},
				}
			}
			synthesized = append(synthesized, v)
		}

		if hasFormView && !formDropped {
			synthesized = append(synthesized, manifest.View{
				Name:     formName,
				Type:     "form",
				Resource: qualifiedModel,
				Label:    displayLabel(md, md.Label),
				Sections: []manifest.FormSection{{Fields: synthesizeFormFields(md)}},
			})
		}

		if md.NavDecl != nil {
			navRoute := modulePathPrefix(moduleName, moduleType) + "/" + pluralPathSegment(md)
			mergedNav = mergeNavItem(mergedNav, md.NavDecl, manifest.NavItem{
				Label: md.NavDecl.Label,
				View:  effectiveListName,
				Route: navRoute,
			})
		}
	}

	return synthesized, suppressed, mergedNav, nil
}

// resolveViewName reports the name a reference to this (resource, type)
// pair should actually use, and whether the synthesized candidateName
// loses to an already-existing hand-declared view. A same-Name collision
// keeps candidateName itself (the hand-declared view already claims that
// exact name); a same-(resource,type) collision under a different name
// hands back that winning view's own name instead, so callers referencing
// this view (a list's "New X" action, a .Nav() item) always point at
// whatever view is actually live.
func resolveViewName(candidateName, resource, viewType string, existingByName map[string]bool, existingByResourceType map[string]string) (effectiveName string, suppressed bool) {
	if existingByName[candidateName] {
		return candidateName, true
	}
	if name, ok := existingByResourceType[resource+"\x00"+viewType]; ok {
		return name, true
	}
	return candidateName, false
}

// displayLabel falls back to the model's bare resource segment when label
// isn't set.
func displayLabel(md model.ModelDeclaration, label string) string {
	if label != "" {
		return label
	}
	return bareNameSegment(md.Name)
}

// cloneNav returns an independent copy of navigation — including each
// group's Children slice — so a caller-owned slice is never mutated by
// mergeNavItem's in-place appends.
func cloneNav(navigation []manifest.NavGroup) []manifest.NavGroup {
	cloned := make([]manifest.NavGroup, len(navigation))
	for i, g := range navigation {
		g.Children = append([]manifest.NavItem(nil), g.Children...)
		cloned[i] = g
	}
	return cloned
}

// mergeNavItem appends item to the NavGroup named decl.Group, creating
// that group (at decl.Order) if this is the first item to claim it —
// across every model in a module sharing a group label, not just within
// one model's own call.
func mergeNavItem(navigation []manifest.NavGroup, decl *model.NavDeclaration, item manifest.NavItem) []manifest.NavGroup {
	for i := range navigation {
		if navigation[i].Label == decl.Group {
			navigation[i].Children = append(navigation[i].Children, item)
			return navigation
		}
	}
	return append(navigation, manifest.NavGroup{
		Label:    decl.Group,
		Order:    decl.Order,
		Children: []manifest.NavItem{item},
	})
}

// columnType maps a field's Kind onto the ListColumn/FormField "type"
// string per go-sdk-reference.md §22 "EnableViews"'s documented mapping
// table. A Kind absent from that table (UUID, JSONB, Bytea, Enum,
// Sequence, Time, One2Many, DynamicLink — and Many2Many, not yet
// representable as a FieldDef at all) reports ok=false: the field is left
// out of the synthesized view entirely rather than guessing at a type the
// table doesn't define.
func columnType(kind model.FieldKind) (t string, ok bool) {
	switch kind {
	case model.KindChar, model.KindText, model.KindSelection:
		return "text", true
	case model.KindBoolean:
		return "boolean", true
	case model.KindDate:
		return "date", true
	case model.KindTimestampTZ:
		return "datetime", true
	case model.KindInteger, model.KindBigInt, model.KindDecimal, model.KindFloat:
		return "number", true
	case model.KindMany2One:
		return "relation", true
	default:
		return "", false
	}
}

// isStandardField reports whether name is one of the four
// WithStandardFields columns EnableViews always excludes
// (go-sdk-reference.md §22 "EnableViews"). "id", "created_at", and
// "updated_at" are deliberately not in this set — their Kinds (UUID,
// TimestampTZ) determine inclusion via columnType instead, the same as
// any module-declared field.
func isStandardField(name string) bool {
	switch name {
	case "tenant_id", "deleted_at", "created_by", "etag":
		return true
	default:
		return false
	}
}

func synthesizeColumns(md model.ModelDeclaration) []manifest.ListColumn {
	var columns []manifest.ListColumn
	for _, f := range md.Fields {
		if isStandardField(f.Name) {
			continue
		}
		t, ok := columnType(f.Def.Kind)
		if !ok {
			continue
		}
		columns = append(columns, manifest.ListColumn{
			Field:   f.Name,
			Type:    t,
			Primary: f.Def.IsPrimary,
		})
	}
	return columns
}

func synthesizeFormFields(md model.ModelDeclaration) []manifest.FormField {
	var fields []manifest.FormField
	for _, f := range md.Fields {
		if isStandardField(f.Name) {
			continue
		}
		t, ok := columnType(f.Def.Kind)
		if !ok {
			continue
		}
		fields = append(fields, manifest.FormField{
			Field:    f.Name,
			Type:     t,
			Required: f.Def.IsRequired,
			Readonly: f.Def.IsComputed || f.Def.IsReadonly,
		})
	}
	return fields
}
