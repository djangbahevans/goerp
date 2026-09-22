package manifest

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rs/zerolog/log"
)

// validDefaultRoles is the fixed role vocabulary manifest-spec.md §7's
// `default_roles` may reference. "superadmin" is deliberately excluded —
// auth-internals.md §10 documents it as the platform-operator credential,
// not a `roles` table row, so it can never be a seeding target.
var validDefaultRoles = []string{"user", "admin", "portal", "public"}

// validatePermissions enforces manifest-spec.md §7's permission declaration
// rules that aren't expressible as a plain field-level validate tag: each
// `name` must be `{module}:{resource}:{action}`, each segment matching the
// same character class as the manifest's own top-level `name`, and unique
// within this manifest. Cross-module name uniqueness isn't checked here — a
// manifest is validated in isolation, before any other module is loaded —
// that's loader.LoadAll's job instead. `default_roles` values outside the
// fixed role vocabulary only log a warning (manifest-spec.md §28) and never
// block load, since an unknown role there just means that grant is skipped
// at tenant provisioning, not a broken manifest.
func validatePermissions(m Manifest) error {
	var violations []string
	reject := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf(format, args...))
	}

	seenNames := make(map[string]bool, len(m.Permissions))
	for _, p := range m.Permissions {
		if !isPermissionName(p.Name) {
			reject("permission %q: name must be {module}:{resource}:{action}, each segment lowercase alphanumeric/underscore starting with a letter", p.Name)
			continue
		}

		if seenNames[p.Name] {
			reject("permission %q: name must be unique within this manifest's permissions", p.Name)
			continue
		}
		seenNames[p.Name] = true

		for _, role := range p.DefaultRoles {
			if !slices.Contains(validDefaultRoles, role) {
				log.Warn().Str("module", m.Name).Str("permission", p.Name).Str("role", role).
					Msg("default_roles references an unknown role name")
			}
		}
	}

	if len(violations) == 0 {
		return nil
	}
	return errors.New(strings.Join(violations, "; "))
}

// isPermissionName reports whether name has exactly three `:`-delimited
// segments, each matching the manifest's own top-level `name` character
// class (validNameRegex). Unlike a policy name (splitPolicyName), a
// permission name's module segment is never required to equal the
// declaring module's own name — permissions are referenced across module
// boundaries (a policy in any manifest can name any declared permission in
// applies_to), so there's no "own module" for the segment to match.
func isPermissionName(name string) bool {
	parts := strings.Split(name, ":")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if !validNameRegex.MatchString(p) {
			return false
		}
	}
	return true
}
