package perm

import (
	"encoding/json/v2"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/declare"
)

func mustPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		t.Helper()
		got := recover()
		if got == nil {
			t.Fatalf("no panic, want one containing %q", want)
		}
		if msg, _ := got.(string); !strings.Contains(msg, want) {
			t.Errorf("panic %q does not contain %q", msg, want)
		}
	}()
	fn()
}

func TestDefine_CarriesNameDescriptionCategoryAndRoles(t *testing.T) {
	p := Define("contacts:contact:financials_read",
		Description("View a contact's credit limit and balances"),
		Category("Contacts"),
		DefaultRoles(Admin, User),
	)

	if p.Name() != "contacts:contact:financials_read" || p.String() != p.Name() {
		t.Errorf("Name/String = %q/%q", p.Name(), p.String())
	}
	if p.Description() != "View a contact's credit limit and balances" || p.Category() != "Contacts" {
		t.Errorf("Description/Category = %q/%q", p.Description(), p.Category())
	}
	if want := []Role{Admin, User}; !slices.Equal(p.DefaultRoles(), want) {
		t.Errorf("DefaultRoles = %v, want %v", p.DefaultRoles(), want)
	}
	if p.IsRef() {
		t.Error("a defined permission reports IsRef")
	}
}

func TestDefine_DefaultRolesIsACopy(t *testing.T) {
	p := Define("contacts:contact:copy_check", Description("D"), DefaultRoles(User))
	p.DefaultRoles()[0] = Public

	if got := p.DefaultRoles(); got[0] != User {
		t.Errorf("DefaultRoles mutated through the accessor: %v", got)
	}
}

func TestDefine_RejectsInvalidDefinitions(t *testing.T) {
	tests := []struct {
		name string
		want string
		fn   func()
	}{
		{"two segments", "must be {module}:{resource}:{action}", func() { Define("contacts:read", Description("D")) }},
		{"four segments", "must be {module}:{resource}:{action}", func() { Define("a:b:c:d", Description("D")) }},
		{"uppercase", "must be {module}:{resource}:{action}", func() { Define("Contacts:contact:read", Description("D")) }},
		{"empty segment", "must be {module}:{resource}:{action}", func() { Define("contacts::read", Description("D")) }},
		{"digit-led segment", "must be {module}:{resource}:{action}", func() { Define("contacts:contact:1read", Description("D")) }},
		{"empty name", "must be {module}:{resource}:{action}", func() { Define("", Description("D")) }},
		{"missing description", "needs perm.Description", func() { Define("contacts:contact:read") }},
		{"unknown role", `unknown default role "root"`, func() { Define("contacts:contact:read", Description("D"), DefaultRoles("root")) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { mustPanic(t, tc.want, tc.fn) })
	}
}

func TestRef_MarksPermissionAsReferenced(t *testing.T) {
	p := Ref("contacts:contact:read")

	if p.Name() != "contacts:contact:read" || !p.IsRef() {
		t.Errorf("Name/IsRef = %q/%v", p.Name(), p.IsRef())
	}
}

func TestRef_RejectsMalformedName(t *testing.T) {
	mustPanic(t, "must be {module}:{resource}:{action}", func() { Ref("contacts") })
}

func TestDefinePolicy_AppliesToDefinedOrReferencedPermission(t *testing.T) {
	defined := Define("sales:order:read", Description("D"))
	ref := Ref("contacts:contact:read")

	for _, applies := range []Permission{defined, ref} {
		p := DefinePolicy("sales:order:own_only", applies, "record.owner_id = current_user.id", Description("Own only"))

		if p.AppliesTo().Name() != applies.Name() || p.Name() != "sales:order:own_only" {
			t.Errorf("Name/AppliesTo = %q/%v", p.Name(), p.AppliesTo())
		}
		if p.Condition() != "record.owner_id = current_user.id" || p.Description() != "Own only" {
			t.Errorf("Condition/Description = %q/%q", p.Condition(), p.Description())
		}
	}
}

func TestDefinePolicy_RejectsInvalidDefinitions(t *testing.T) {
	read := Define("sales:order:read", Description("D"))
	tests := []struct {
		name string
		want string
		fn   func()
	}{
		{"bad name", "must be {module}:{resource}:{action}", func() { DefinePolicy("own_only", read, "true") }},
		{"long name", "exceeds 63 bytes", func() { DefinePolicy("sales:order:"+strings.Repeat("a", 52), read, "true") }},
		{"zero permission", "needs a permission to apply to", func() { DefinePolicy("sales:order:own_only", Permission{}, "true") }},
		{"empty condition", "needs a condition", func() { DefinePolicy("sales:order:own_only", read, "") }},
		{"category", "apply only to perm.Define", func() { DefinePolicy("sales:order:own_only", read, "true", Category("C")) }},
		{"default roles", "apply only to perm.Define", func() { DefinePolicy("sales:order:own_only", read, "true", DefaultRoles(User)) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { mustPanic(t, tc.want, tc.fn) })
	}
}

func TestDefinePolicy_AcceptsNameAtLimit(t *testing.T) {
	read := Define("sales:order:read", Description("D"))

	DefinePolicy("sales:order:"+strings.Repeat("a", 51), read, "true")
}

func TestDeclarations_AreRecordedInTheRegistry(t *testing.T) {
	p := Define("regtest:thing:read", Description("Read things"), Category("Things"), DefaultRoles(User, Admin))
	r := Ref("regtest_other:thing:read")
	DefinePolicy("regtest:thing:own_only", p, "record.a = 1", Description("Own"))
	DefinePolicy("regtest:thing:ref_scoped", r, "record.a = 2")

	data, err := declare.Export()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string][]map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	find := func(kind, name string) map[string]any {
		t.Helper()
		for _, d := range got[kind] {
			if d["name"] == name {
				return d
			}
		}
		t.Fatalf("no %s declaration named %q in %s", kind, name, data)
		return nil
	}

	def := find(KindPermission, "regtest:thing:read")
	if def["description"] != "Read things" || def["category"] != "Things" ||
		!slices.Equal(def["default_roles"].([]any), []any{"user", "admin"}) {
		t.Errorf("permission declaration = %v", def)
	}
	find(KindPermissionRef, "regtest_other:thing:read")
	pol := find(KindPolicy, "regtest:thing:own_only")
	if pol["applies_to"] != "regtest:thing:read" || pol["condition"] != "record.a = 1" || pol["description"] != "Own" {
		t.Errorf("policy declaration = %v", pol)
	}
	if find(KindPolicy, "regtest:thing:ref_scoped")["applies_to"] != "regtest_other:thing:read" {
		t.Error("policy over a Ref does not record the referenced name")
	}
}

func TestPackageLinksNoHostCallLayer(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for dep := range strings.Lines(string(out)) {
		dep = strings.TrimSpace(dep)
		for _, banned := range []string{"/sdk/go/db", "/sdk/go/engine", "/sdk/go/orm", "/sdk/go/authz", "/sdk/go/internal/hostcall"} {
			if strings.HasSuffix(dep, banned) {
				t.Errorf("perm depends on %s", dep)
			}
		}
	}
}
