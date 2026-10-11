package manifest

import (
	"errors"
	"fmt"
	"strings"
)

// superadmin is a platform credential, not a tenant role or grant-seeding target.
var validDefaultRoles = []string{"user", "admin", "portal", "public"}

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
	}

	seenUses := make(map[string]bool, len(m.UsesPermissions))
	for _, name := range m.UsesPermissions {
		switch owner, _, _ := strings.Cut(name, ":"); {
		case !isPermissionName(name):
			reject("uses_permissions %q: name must be {module}:{resource}:{action}, each segment lowercase alphanumeric/underscore starting with a letter", name)
		case owner == m.Name:
			reject("uses_permissions %q: module segment is this manifest's own name; declare the permission in permissions instead", name)
		case seenUses[name]:
			reject("uses_permissions %q: name must be unique within uses_permissions", name)
		case seenNames[name]:
			reject("uses_permissions %q: name is also declared in this manifest's permissions", name)
		}
		seenUses[name] = true
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
