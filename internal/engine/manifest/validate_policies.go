package manifest

import (
	"errors"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/domain"
)

// maxPolicyNameBytes is Postgres's NAMEDATALEN-1: the longest identifier
// CREATE POLICY can store without silently truncating it.
// SyncRLSPolicies installs a policy's Postgres identifier from `name`
// verbatim (internal/engine/schema/rls.go), and reconciliation's
// desired-map lookup depends on the stored name never differing from the
// declared one — a truncated name would drop the policy in the very sync
// call that created it.
const maxPolicyNameBytes = 63

// validatePolicies enforces manifest-spec.md §8's load-time rules for ABAC
// policies: `name` must be `{module}:{resource}:{policy_name}` with
// `module` equal to this manifest's own declared name and the whole name
// within maxPolicyNameBytes, `condition` must parse under the domain
// expression grammar, and `applies_to` must name a permission the same
// manifest declares. Cross-module permission references aren't possible
// to check here — a manifest is validated in isolation, before any other
// module is loaded — so this only checks a policy against its own
// manifest's `permissions`.
func validatePolicies(m Manifest) error {
	var violations []string
	reject := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf(format, args...))
	}

	declaredPermissions := make(map[string]bool, len(m.Permissions))
	for _, p := range m.Permissions {
		declaredPermissions[p.Name] = true
	}

	for _, policy := range m.Policies {
		if len(policy.Name) > maxPolicyNameBytes {
			reject("policy %q: name exceeds %d bytes, the longest Postgres RLS policy identifier can be", policy.Name, maxPolicyNameBytes)
			continue
		}

		module, ok := splitPolicyName(policy.Name)
		if !ok {
			reject("policy %q: name must be {module}:{resource}:{policy_name}, each segment lowercase alphanumeric/underscore starting with a letter", policy.Name)
			continue
		}
		if module != m.Name {
			reject("policy %q: name's module segment %q must match this manifest's own name %q", policy.Name, module, m.Name)
			continue
		}

		if !declaredPermissions[policy.AppliesTo] {
			reject("policy %q: applies_to %q must reference a permission declared in this manifest's permissions", policy.Name, policy.AppliesTo)
			continue
		}

		if _, err := domain.Parse(policy.Condition); err != nil {
			reject("policy %q: condition failed to parse: %v", policy.Name, err)
		}
	}

	if len(violations) == 0 {
		return nil
	}
	return errors.New(strings.Join(violations, "; "))
}

// splitPolicyName reports whether name has exactly three `:`-delimited
// segments, each matching the same character class the manifest's own
// top-level `name` field is validated against (validNameRegex), and
// returns the first (module) segment when it does.
func splitPolicyName(name string) (module string, ok bool) {
	parts := strings.Split(name, ":")
	if len(parts) != 3 {
		return "", false
	}
	for _, p := range parts {
		if !validNameRegex.MatchString(p) {
			return "", false
		}
	}
	return parts[0], true
}
