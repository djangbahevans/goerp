package manifest

import (
	"bytes"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// captureLog temporarily redirects the global zerolog logger to a buffer
// for the duration of a test, restoring it on cleanup (mirrors
// loader's own view_extension_conflicts_test.go helper — no shared
// testing package precedent for this yet).
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	original := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = original })
	return &buf
}

func TestLoadManifest_PermissionValidName_Passes(t *testing.T) {
	m := manifestWithPolicies(t,
		[]map[string]any{{"name": "demo:order:read", "description": "Read orders"}},
		nil,
	)

	if _, err := Load(m); err != nil {
		t.Fatalf("expected manifest with a valid permission name to load, got %v", err)
	}
}

func TestLoadManifest_PermissionNameNotThreeSegments_Rejected(t *testing.T) {
	m := manifestWithPolicies(t,
		[]map[string]any{{"name": "demo:read", "description": "Read orders"}},
		nil,
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest with a two-segment permission name to be rejected")
	}
	if !strings.Contains(err.Error(), "name must be {module}:{resource}:{action}") {
		t.Fatalf("expected error to describe the required name format, got: %v", err)
	}
}

func TestLoadManifest_PermissionNameUppercaseSegment_Rejected(t *testing.T) {
	m := manifestWithPolicies(t,
		[]map[string]any{{"name": "Demo:order:read", "description": "Read orders"}},
		nil,
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest with an uppercase permission name segment to be rejected")
	}
	if !strings.Contains(err.Error(), `permission "Demo:order:read"`) {
		t.Fatalf("expected error to name the rejected permission, got: %v", err)
	}
}

func TestLoadManifest_PermissionNameDuplicateWithinManifest_Rejected(t *testing.T) {
	m := manifestWithPolicies(t,
		[]map[string]any{
			{"name": "demo:order:read", "description": "Read orders"},
			{"name": "demo:order:read", "description": "Read orders again"},
		},
		nil,
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest declaring the same permission name twice to be rejected")
	}
	if !strings.Contains(err.Error(), "must be unique within this manifest's permissions") {
		t.Fatalf("expected error to mention the uniqueness rule, got: %v", err)
	}
}

func TestLoadManifest_PermissionModuleSegmentNeedNotMatchManifestName_Passes(t *testing.T) {
	// Unlike a policy name, a permission's module segment isn't required to
	// equal the declaring manifest's own name — minimalManifestFields()'s
	// name is "demo", not "sales".
	m := manifestWithPolicies(t,
		[]map[string]any{{"name": "sales:order:read", "description": "Read orders"}},
		nil,
	)

	if _, err := Load(m); err != nil {
		t.Fatalf("expected a permission name with a different module segment to load, got %v", err)
	}
}

func TestLoadManifest_PermissionDefaultRolesUnknownValue_WarnsWithoutFailing(t *testing.T) {
	buf := captureLog(t)

	m := manifestWithPolicies(t,
		[]map[string]any{{
			"name":          "demo:order:read",
			"description":   "Read orders",
			"default_roles": []string{"user", "bogus"},
		}},
		nil,
	)

	if _, err := Load(m); err != nil {
		t.Fatalf("expected an unknown default_roles value to warn, not fail load, got %v", err)
	}

	if !strings.Contains(buf.String(), "default_roles references an unknown role name") {
		t.Fatalf("expected a warning naming the unknown role, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"role":"bogus"`) {
		t.Fatalf("expected the warning to name the unknown role value, got: %s", buf.String())
	}
}

func TestLoadManifest_PermissionDefaultRolesKnownValues_NoWarning(t *testing.T) {
	buf := captureLog(t)

	m := manifestWithPolicies(t,
		[]map[string]any{{
			"name":          "demo:order:read",
			"description":   "Read orders",
			"default_roles": []string{"user", "admin", "portal", "public"},
		}},
		nil,
	)

	if _, err := Load(m); err != nil {
		t.Fatalf("expected valid default_roles to load, got %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no warning for known default_roles values, got: %s", buf.String())
	}
}

func TestLoadManifest_PermissionCategoryOmitted_DefaultsToDisplayName(t *testing.T) {
	fields := minimalManifestFields()
	fields["permissions"] = []map[string]any{{"name": "demo:order:read", "description": "Read orders"}}

	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	got, err := Load(raw)
	if err != nil {
		t.Fatalf("expected manifest to load, got %v", err)
	}
	if len(got.Permissions) != 1 {
		t.Fatalf("expected 1 permission, got %d", len(got.Permissions))
	}
	if got.Permissions[0].Category != got.DisplayName {
		t.Fatalf("Category = %q, want it defaulted to DisplayName %q", got.Permissions[0].Category, got.DisplayName)
	}
}

func TestLoadManifest_PermissionCategoryExplicit_Preserved(t *testing.T) {
	fields := minimalManifestFields()
	fields["permissions"] = []map[string]any{{
		"name":        "demo:order:read",
		"description": "Read orders",
		"category":    "Orders",
	}}

	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	got, err := Load(raw)
	if err != nil {
		t.Fatalf("expected manifest to load, got %v", err)
	}
	if got.Permissions[0].Category != "Orders" {
		t.Fatalf("Category = %q, want explicit value %q preserved", got.Permissions[0].Category, "Orders")
	}
}

func manifestWithUsesPermissions(t *testing.T, permissions []map[string]any, uses []string) []byte {
	t.Helper()
	fields := minimalManifestFields()
	if permissions != nil {
		fields["permissions"] = permissions
	}
	fields["uses_permissions"] = uses
	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return m
}

func TestLoadManifest_UsesPermissions_Valid_Passes(t *testing.T) {
	m := manifestWithUsesPermissions(t, nil, []string{"contacts:contact:read", "sales:order:read"})

	mf, err := Load(m)
	if err != nil {
		t.Fatalf("Load = %v", err)
	}
	if len(mf.UsesPermissions) != 2 {
		t.Errorf("UsesPermissions = %v, want both entries", mf.UsesPermissions)
	}
}

func TestLoadManifest_UsesPermissions_Rejected(t *testing.T) {
	tests := []struct {
		name        string
		permissions []map[string]any
		uses        []string
		want        string
	}{
		{"malformed name", nil, []string{"contacts:read"}, "must be {module}:{resource}:{action}"},
		{"own module", nil, []string{"demo:order:read"}, "module segment is this manifest's own name"},
		{"duplicate", nil, []string{"contacts:contact:read", "contacts:contact:read"}, "must be unique within uses_permissions"},
		{"also declared", []map[string]any{{"name": "contacts:contact:read", "description": "Read"}}, []string{"contacts:contact:read"}, "also declared in this manifest's permissions"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(manifestWithUsesPermissions(t, tc.permissions, tc.uses))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
