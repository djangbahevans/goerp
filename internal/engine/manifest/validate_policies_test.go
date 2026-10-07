package manifest

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func manifestWithPolicies(t *testing.T, permissions, policies []map[string]any) []byte {
	t.Helper()
	fields := minimalManifestFields()
	if permissions != nil {
		fields["permissions"] = permissions
	}
	if policies != nil {
		fields["policies"] = policies
	}
	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return m
}

func TestLoadManifest_PolicyAppliesToDeclaredPermission_Passes(t *testing.T) {
	m := manifestWithPolicies(t,
		[]map[string]any{{"name": "demo:order:read", "description": "Read orders"}},
		[]map[string]any{{
			"name":        "demo:order:own_only",
			"description": "Own orders only",
			"applies_to":  "demo:order:read",
			"condition":   "record.salesperson_id = current_user.contact_id OR user_has_role('sales_manager')",
		}},
	)

	if _, err := Load(m); err != nil {
		t.Fatalf("expected manifest with a valid policy to load, got %v", err)
	}
}

func TestLoadManifest_PolicyAppliesToUndeclaredPermission_Rejected(t *testing.T) {
	m := manifestWithPolicies(t,
		[]map[string]any{{"name": "demo:order:read", "description": "Read orders"}},
		[]map[string]any{{
			"name":        "demo:order:own_only",
			"description": "Own orders only",
			"applies_to":  "demo:order:write", // not declared above
			"condition":   "record.salesperson_id = current_user.contact_id",
		}},
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest with an undeclared applies_to to be rejected")
	}
	if !strings.Contains(err.Error(), "applies_to") {
		t.Fatalf("expected error to mention applies_to, got: %v", err)
	}
}

func TestLoadManifest_PolicyConditionFailsToParse_Rejected(t *testing.T) {
	m := manifestWithPolicies(t,
		[]map[string]any{{"name": "demo:order:read", "description": "Read orders"}},
		[]map[string]any{{
			"name":        "demo:order:own_only",
			"description": "Own orders only",
			"applies_to":  "demo:order:read",
			"condition":   "record.salesperson_id ===",
		}},
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest with an unparseable condition to be rejected")
	}
	if !strings.Contains(err.Error(), "condition") {
		t.Fatalf("expected error to mention condition, got: %v", err)
	}
}

func TestLoadManifest_PolicyNameWrongModuleSegment_Rejected(t *testing.T) {
	m := manifestWithPolicies(t,
		[]map[string]any{{"name": "demo:order:read", "description": "Read orders"}},
		[]map[string]any{{
			"name":        "sales:order:own_only", // minimalManifestFields()'s own name is "demo", not "sales"
			"description": "Own orders only",
			"applies_to":  "demo:order:read",
			"condition":   "record.salesperson_id = current_user.contact_id",
		}},
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest with a policy name whose module segment doesn't match this manifest's own name to be rejected")
	}
	if !strings.Contains(err.Error(), `module segment "sales"`) {
		t.Fatalf("expected error to mention the mismatched module segment, got: %v", err)
	}
}

func TestLoadManifest_PolicyNameNotThreeSegments_Rejected(t *testing.T) {
	m := manifestWithPolicies(t,
		[]map[string]any{{"name": "demo:order:read", "description": "Read orders"}},
		[]map[string]any{{
			"name":        "demo:own_only", // only two segments
			"description": "Own orders only",
			"applies_to":  "demo:order:read",
			"condition":   "record.salesperson_id = current_user.contact_id",
		}},
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest with a two-segment policy name to be rejected")
	}
	if !strings.Contains(err.Error(), "name must be {module}:{resource}:{policy_name}") {
		t.Fatalf("expected error to describe the required name format, got: %v", err)
	}
}

func TestLoadManifest_PolicyNameOverMaxBytes_Rejected(t *testing.T) {
	longPolicyName := strings.Repeat("a", 60) // "demo:order:" (11 bytes) + 60 = 71 bytes, over the 63 limit
	m := manifestWithPolicies(t,
		[]map[string]any{{"name": "demo:order:read", "description": "Read orders"}},
		[]map[string]any{{
			"name":        "demo:order:" + longPolicyName,
			"description": "Own orders only",
			"applies_to":  "demo:order:read",
			"condition":   "record.salesperson_id = current_user.contact_id",
		}},
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest with an over-length policy name to be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds 63 bytes") {
		t.Fatalf("expected error to mention the 63-byte limit, got: %v", err)
	}
}

func TestLoadManifest_NoPolicies_Passes(t *testing.T) {
	m := manifestWithPolicies(t, nil, nil)
	if _, err := Load(m); err != nil {
		t.Fatalf("expected manifest with no policies to load, got %v", err)
	}
}

func TestLoadManifest_PolicyAppliesToUsedPermission_Passes(t *testing.T) {
	fields := minimalManifestFields()
	fields["uses_permissions"] = []string{"contacts:contact:read"}
	fields["policies"] = []map[string]any{{
		"name":       "demo:order:contact_scoped",
		"applies_to": "contacts:contact:read",
		"condition":  "record.owner_id = current_user.id",
	}}
	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	if _, err := Load(m); err != nil {
		t.Fatalf("Load = %v, want a policy over a uses_permissions entry to load", err)
	}
}

func TestLoadManifest_PolicyCombine(t *testing.T) {
	tests := []struct {
		combine string
		wantErr string
	}{
		{"", ""},
		{"OR", ""},
		{"AND", ""},
		{"and", `combine "and" must be "OR" or "AND"`},
		{"XOR", `combine "XOR" must be "OR" or "AND"`},
	}
	for _, tc := range tests {
		t.Run("combine="+tc.combine, func(t *testing.T) {
			policy := map[string]any{
				"name":       "demo:order:own_only",
				"applies_to": "demo:order:read",
				"condition":  "record.owner_id = current_user.id",
			}
			if tc.combine != "" {
				policy["combine"] = tc.combine
			}
			m := manifestWithPolicies(t,
				[]map[string]any{{"name": "demo:order:read", "description": "Read orders"}},
				[]map[string]any{policy})

			mf, err := Load(m)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Load = %v, want it to load", err)
				}
				if got := mf.Policies[0].Restrictive(); got != (tc.combine == "AND") {
					t.Errorf("Restrictive() = %v for combine %q", got, tc.combine)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}
