package module

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func manifestDoc(t *testing.T, raw string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return doc
}

func TestPermissionsCollector_ProducesEntriesSortedByName(t *testing.T) {
	d := decls(t, map[string][]any{
		perm.KindPermission: {
			perm.PermissionDeclaration{Name: "widgets:widget:write", Description: "Edit"},
			perm.PermissionDeclaration{Name: "widgets:widget:read", Description: "View", Category: "Widgets", DefaultRoles: []string{"user", "admin"}},
		},
		perm.KindPermissionRef: {perm.RefDeclaration{Name: "contacts:contact:read"}},
	})

	want := `[{"name":"widgets:widget:read","description":"View","category":"Widgets","default_roles":["user","admin"]},` +
		`{"name":"widgets:widget:write","description":"Edit"}]`
	if got := collectJSON(t, permissionsCollector{}, d, ModuleInfo{Name: "widgets"}); got != want {
		t.Errorf("permissions = %s, want %s", got, want)
	}
}

func TestPermissionsCollector_NothingDeclaredLeavesTheKeyOut(t *testing.T) {
	if got := collectJSON(t, permissionsCollector{}, Declarations{}, ModuleInfo{Name: "widgets"}); got != "[]" {
		t.Errorf("permissions = %s, want an empty array", got)
	}
}

func TestPermissionsCollector_OwnershipFailures(t *testing.T) {
	tests := []struct {
		name string
		d    map[string][]any
		want string
	}{
		{
			name: "define in another module's namespace",
			d:    map[string][]any{perm.KindPermission: {perm.PermissionDeclaration{Name: "contacts:contact:read", Description: "D"}}},
			want: `perm.Define module segment "contacts" must be the module's own name "widgets"`,
		},
		{
			name: "ref to the module's own namespace",
			d:    map[string][]any{perm.KindPermissionRef: {perm.RefDeclaration{Name: "widgets:widget:read"}}},
			want: `perm.Ref names the module's own permission`,
		},
		{
			name: "defined twice",
			d: map[string][]any{perm.KindPermission: {
				perm.PermissionDeclaration{Name: "widgets:widget:read", Description: "D"},
				perm.PermissionDeclaration{Name: "widgets:widget:read", Description: "D"},
			}},
			want: `"widgets:widget:read" is declared more than once with perm.Define`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := permissionsCollector{}.Collect(decls(t, tt.d), ModuleInfo{Name: "widgets"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Collect = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestPermissionsCollector_LintsHandWrittenBlocks(t *testing.T) {
	d := decls(t, map[string][]any{
		perm.KindPermission:    {perm.PermissionDeclaration{Name: "widgets:widget:read", Description: "D"}},
		perm.KindPermissionRef: {perm.RefDeclaration{Name: "contacts:contact:read"}},
	})
	info := func(raw string) ModuleInfo { return ModuleInfo{Name: "widgets", Manifest: manifestDoc(t, raw)} }

	valid := info(`{
		"views": [{"name": "widget_list", "permission": "widgets:widget:read", "actions": [{"permission": "contacts:contact:read"}]}],
		"navigation": [{"label": "Widgets", "permission": "widgets:widget:read", "children": [{"label": "All", "permission": "contacts:contact:read"}]}],
		"reports": [{"name": "summary", "permissions": ["widgets:widget:read", "contacts:contact:read"]}],
		"actions": [{"permission": "not:checked:here"}]
	}`)
	if _, err := (permissionsCollector{}).Collect(d, valid); err != nil {
		t.Fatalf("Collect on valid blocks: %v", err)
	}

	invalid := info(`{
		"views": [{"name": "widget_list", "actions": [{"permission": "widgets:widget:typo"}]}],
		"navigation": [{"label": "Widgets", "children": [{"label": "All", "permission": "widgets:widget:gone"}]}],
		"reports": [{"name": "summary", "permissions": ["widgets:widget:read", "Bad Name"]}]
	}`)
	_, err := permissionsCollector{}.Collect(d, invalid)
	if err == nil {
		t.Fatal("Collect on undeclared names = nil error")
	}
	for _, want := range []string{
		`view "widget_list": actions[0].permission names permission "widgets:widget:typo"`,
		`navigation "Widgets": children[0].permission names permission "widgets:widget:gone"`,
		`report "summary": permissions[1] names permission "Bad Name"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestPoliciesCollector_ProducesEntriesAndChecksAppliesTo(t *testing.T) {
	d := decls(t, map[string][]any{
		perm.KindPermission:    {perm.PermissionDeclaration{Name: "widgets:widget:read", Description: "D"}},
		perm.KindPermissionRef: {perm.RefDeclaration{Name: "contacts:contact:read"}},
		perm.KindPolicy: {
			perm.PolicyDeclaration{Name: "widgets:widget:own_only", AppliesTo: "widgets:widget:read", Condition: "record.a = 1", Description: "Own"},
			perm.PolicyDeclaration{Name: "widgets:contact:scoped", AppliesTo: "contacts:contact:read", Condition: "true"},
		},
	})

	want := `[{"name":"widgets:contact:scoped","applies_to":"contacts:contact:read","condition":"true"},` +
		`{"name":"widgets:widget:own_only","description":"Own","applies_to":"widgets:widget:read","condition":"record.a = 1"}]`
	if got := collectJSON(t, policiesCollector{}, d, ModuleInfo{Name: "widgets"}); got != want {
		t.Errorf("policies = %s, want %s", got, want)
	}
}

func TestPoliciesCollector_Failures(t *testing.T) {
	defined := perm.PermissionDeclaration{Name: "widgets:widget:read", Description: "D"}
	tests := []struct {
		name     string
		policies []any
		want     string
	}{
		{
			name:     "applies to an unknown permission",
			policies: []any{perm.PolicyDeclaration{Name: "widgets:widget:p", AppliesTo: "widgets:widget:nope", Condition: "true"}},
			want:     `applies to "widgets:widget:nope", which the module neither defines`,
		},
		{
			name:     "other module's namespace",
			policies: []any{perm.PolicyDeclaration{Name: "contacts:widget:p", AppliesTo: "widgets:widget:read", Condition: "true"}},
			want:     `perm.DefinePolicy module segment "contacts" must be the module's own name "widgets"`,
		},
		{
			name: "declared twice",
			policies: []any{
				perm.PolicyDeclaration{Name: "widgets:widget:p", AppliesTo: "widgets:widget:read", Condition: "true"},
				perm.PolicyDeclaration{Name: "widgets:widget:p", AppliesTo: "widgets:widget:read", Condition: "false"},
			},
			want: `"widgets:widget:p" is declared more than once with perm.DefinePolicy`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := decls(t, map[string][]any{perm.KindPermission: {defined}, perm.KindPolicy: tt.policies})

			_, err := policiesCollector{}.Collect(d, ModuleInfo{Name: "widgets"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Collect = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestUsesPermissionsCollector_ListsSortedUniqueRefs(t *testing.T) {
	d := decls(t, map[string][]any{perm.KindPermissionRef: {
		perm.RefDeclaration{Name: "contacts:contact:write"},
		perm.RefDeclaration{Name: "contacts:contact:read"},
		perm.RefDeclaration{Name: "contacts:contact:write"},
	}})

	want := `["contacts:contact:read","contacts:contact:write"]`
	if got := collectJSON(t, usesPermissionsCollector{}, d, ModuleInfo{Name: "widgets"}); got != want {
		t.Errorf("uses_permissions = %s, want %s", got, want)
	}
}

func TestPoliciesCollector_CarriesCombine(t *testing.T) {
	d := decls(t, map[string][]any{
		perm.KindPermission: {perm.PermissionDeclaration{Name: "widgets:widget:read", Description: "D"}},
		perm.KindPolicy: {
			perm.PolicyDeclaration{Name: "widgets:widget:a_restrictive", AppliesTo: "widgets:widget:read", Condition: "record.a = 1", Combine: "AND"},
			perm.PolicyDeclaration{Name: "widgets:widget:b_default", AppliesTo: "widgets:widget:read", Condition: "record.a = 2"},
		},
	})

	want := `[{"name":"widgets:widget:a_restrictive","applies_to":"widgets:widget:read","condition":"record.a = 1","combine":"AND"},` +
		`{"name":"widgets:widget:b_default","applies_to":"widgets:widget:read","condition":"record.a = 2"}]`
	if got := collectJSON(t, policiesCollector{}, d, ModuleInfo{Name: "widgets"}); got != want {
		t.Errorf("policies = %s, want %s", got, want)
	}
}
