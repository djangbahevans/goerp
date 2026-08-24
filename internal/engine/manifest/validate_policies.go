package manifest

import (
	"errors"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/domain"
)

// validatePolicies enforces manifest-spec.md §8's load-time rules for ABAC
// policies: `condition` must parse under the domain expression grammar,
// and `applies_to` must name a permission the same manifest declares.
// Cross-module permission references aren't possible to check here — a
// manifest is validated in isolation, before any other module is loaded —
// so this only checks a policy against its own manifest's `permissions`.
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
