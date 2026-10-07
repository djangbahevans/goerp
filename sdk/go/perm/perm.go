// Package perm holds typed permission and policy definitions. It is
// host-call-free, so a module's schema package can import it to name a
// permission in a field access declaration (go-sdk-reference.md §11
// "Defining a permission", §22 "Package layout"). Every definition,
// reference and policy is recorded into sdk/go/declare for the manifest's
// permissions, uses_permissions and policies blocks.
package perm

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/djangbahevans/goerp/sdk/go/declare"
)

// Role is a system role a permission can be granted to by default. The
// superadmin role always holds every permission and is not a Role.
type Role string

// Roles accepted by DefaultRoles (manifest-spec.md §7).
const (
	User   Role = "user"
	Admin  Role = "admin"
	Portal Role = "portal"
	Public Role = "public"
)

// maxPolicyNameBytes is the longest identifier Postgres's CREATE POLICY
// stores without truncating it.
const maxPolicyNameBytes = 63

const (
	segment       = `[a-z][a-z0-9_]*`
	errNameFormat = "name %q must be {module}:{resource}:{action}, lowercase letters, digits and underscores"
)

var (
	namePattern = regexp.MustCompile(`^` + segment + `:` + segment + `:` + segment + `$`)
	roles       = []Role{User, Admin, Portal, Public}
)

// Permission is a permission this module defines or references. The zero
// value names nothing and is rejected by DefinePolicy.
type Permission struct {
	name         string
	description  string
	category     string
	defaultRoles []Role
	ref          bool
}

// Name returns the permission's {module}:{resource}:{action} name.
func (p Permission) Name() string { return p.name }

// String returns the permission's name.
func (p Permission) String() string { return p.name }

// Description returns the text shown in the role editor.
func (p Permission) Description() string { return p.description }

// Category returns the role editor grouping label, empty when the module's
// display name applies.
func (p Permission) Category() string { return p.category }

// DefaultRoles returns the roles that hold the permission at tenant
// provisioning.
func (p Permission) DefaultRoles() []Role { return slices.Clone(p.defaultRoles) }

// IsRef reports whether the permission is owned by another module.
func (p Permission) IsRef() bool { return p.ref }

// Policy is an ABAC policy scoping a permission to the records a user may
// reach.
type Policy struct {
	name        string
	appliesTo   Permission
	condition   string
	description string
	combine     CombineMode
}

// Combine returns how the policy joins the others scoping its permission; Or
// when it was declared without a mode.
func (p Policy) Combine() CombineMode {
	if p.combine == "" {
		return Or
	}
	return p.combine
}

// Name returns the policy's name.
func (p Policy) Name() string { return p.name }

// AppliesTo returns the permission the policy scopes.
func (p Policy) AppliesTo() Permission { return p.appliesTo }

// Condition returns the policy's domain expression.
func (p Policy) Condition() string { return p.condition }

// Description returns the policy's description.
func (p Policy) Description() string { return p.description }

// CombineMode is how a policy joins the other policies scoping the same
// permission.
type CombineMode string

// Combine modes accepted by Combine (manifest-spec.md §8). Or, the default,
// makes a policy permissive: one admitting policy is enough. And makes it
// restrictive: it must hold in addition to the permissive ones.
const (
	Or  CombineMode = "OR"
	And CombineMode = "AND"
)

// DefineOption configures Define and DefinePolicy — Description, Category,
// DefaultRoles, Combine.
type DefineOption func(*definition)

type definition struct {
	description  string
	category     string
	defaultRoles []Role
	hasRoles     bool
	hasCategory  bool
	combine      CombineMode
}

// Description sets the text shown in the role editor, or a policy's
// description.
func Description(text string) DefineOption {
	return func(d *definition) { d.description = text }
}

// Category sets the role editor grouping label. Unset defaults to the
// module's display_name.
func Category(label string) DefineOption {
	return func(d *definition) {
		d.category = label
		d.hasCategory = true
	}
}

// Combine sets how a policy joins the others scoping its permission; Or when
// unset. It applies only to DefinePolicy.
func Combine(mode CombineMode) DefineOption {
	return func(d *definition) { d.combine = mode }
}

// DefaultRoles sets the roles that hold the permission at tenant
// provisioning.
func DefaultRoles(r ...Role) DefineOption {
	return func(d *definition) {
		d.defaultRoles = r
		d.hasRoles = true
	}
}

// Declaration kinds recorded into sdk/go/declare for the permissions,
// uses_permissions and policies collectors of `goerp module generate`.
const (
	KindPermission    = "permission"
	KindPermissionRef = "permission_ref"
	KindPolicy        = "policy"
)

// PermissionDeclaration is a Define call as the manifest generator reads it.
type PermissionDeclaration struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Category     string   `json:"category,omitzero"`
	DefaultRoles []string `json:"default_roles,omitzero"`
}

// RefDeclaration is a Ref call as the manifest generator reads it.
type RefDeclaration struct {
	Name string `json:"name"`
}

// PolicyDeclaration is a DefinePolicy call as the manifest generator reads it.
type PolicyDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`
	AppliesTo   string `json:"applies_to"`
	Condition   string `json:"condition"`
	Combine     string `json:"combine,omitzero"`
}

func applyOptions(opts []DefineOption) definition {
	var d definition
	for _, opt := range opts {
		opt(&d)
	}
	return d
}

// Define declares a permission owned by this module, called in init() or a
// package-level var. It panics when name is not {module}:{resource}:{action}
// or no Description is given, a default role is not one of User, Admin,
// Portal and Public, or Combine is given.
func Define(name string, opts ...DefineOption) Permission {
	d := applyOptions(opts)
	switch {
	case !namePattern.MatchString(name):
		panic(fmt.Sprintf("perm.Define: "+errNameFormat, name))
	case d.description == "":
		panic(fmt.Sprintf("perm.Define: %q needs perm.Description", name))
	case d.combine != "":
		panic(fmt.Sprintf("perm.Define: %q: perm.Combine applies only to perm.DefinePolicy", name))
	}
	for _, r := range d.defaultRoles {
		if !slices.Contains(roles, r) {
			panic(fmt.Sprintf("perm.Define: %q has unknown default role %q", name, r))
		}
	}

	p := Permission{
		name:         name,
		description:  d.description,
		category:     d.category,
		defaultRoles: slices.Clone(d.defaultRoles),
	}
	decl := PermissionDeclaration{Name: name, Description: d.description, Category: d.category}
	for _, r := range d.defaultRoles {
		decl.DefaultRoles = append(decl.DefaultRoles, string(r))
	}
	declare.Add(KindPermission, decl)
	return p
}

// Ref declares use of a permission another module owns, called in init() or
// a package-level var. It panics when name is not {module}:{resource}:{action}.
// That the module segment differs from this module's own name is checked by
// `goerp module generate`, which knows the module's name.
func Ref(name string) Permission {
	if !namePattern.MatchString(name) {
		panic(fmt.Sprintf("perm.Ref: "+errNameFormat, name))
	}
	declare.Add(KindPermissionRef, RefDeclaration{Name: name})
	return Permission{name: name, ref: true}
}

// DefinePolicy declares an ABAC policy scoping appliesTo, a permission this
// module defines or a Ref, called in init() or a package-level var. It
// panics when name is not {module}:{resource}:{policy_name} within 63 bytes,
// appliesTo is the zero Permission, condition is empty, Category or
// DefaultRoles is given, or Combine is not Or or And.
func DefinePolicy(name string, appliesTo Permission, condition string, opts ...DefineOption) Policy {
	d := applyOptions(opts)
	switch {
	case !namePattern.MatchString(name):
		panic(fmt.Sprintf("perm.DefinePolicy: "+errNameFormat, name))
	case len(name) > maxPolicyNameBytes:
		panic(fmt.Sprintf("perm.DefinePolicy: name %q exceeds %d bytes", name, maxPolicyNameBytes))
	case appliesTo.name == "":
		panic(fmt.Sprintf("perm.DefinePolicy: %q needs a permission to apply to", name))
	case condition == "":
		panic(fmt.Sprintf("perm.DefinePolicy: %q needs a condition", name))
	case d.hasCategory || d.hasRoles:
		panic(fmt.Sprintf("perm.DefinePolicy: %q: perm.Category and perm.DefaultRoles apply only to perm.Define", name))
	case d.combine != "" && d.combine != Or && d.combine != And:
		panic(fmt.Sprintf("perm.DefinePolicy: %q: combine %q must be perm.Or or perm.And", name, d.combine))
	}

	declare.Add(KindPolicy, PolicyDeclaration{
		Name:        name,
		Description: d.description,
		AppliesTo:   appliesTo.name,
		Condition:   condition,
		Combine:     string(d.combine),
	})
	return Policy{name: name, appliesTo: appliesTo, condition: condition, description: d.description, combine: d.combine}
}
