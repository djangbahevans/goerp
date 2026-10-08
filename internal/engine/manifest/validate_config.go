package manifest

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

var configTypes = []string{"string", "integer", "float", "boolean", "duration", "json", "string[]", "integer[]", "float[]"}

// validateConfigSchema enforces manifest-spec.md §17's per-manifest config
// rules: unique short keys, a known type, a duration default and bounds that
// parse, and uses_config entries that are full {module}.{key} names of a
// declared dependency other than the module itself, with a known type. Whether
// the owner is loaded and declares the key with that type needs the full loaded
// module set and lives in internal/engine/loader.
func validateConfigSchema(m Manifest) error {
	var violations []string
	reject := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf(format, args...))
	}

	seen := make(map[string]bool, len(m.ConfigSchema))
	for _, c := range m.ConfigSchema {
		if seen[c.Key] {
			reject("config_schema: key %q declared more than once", c.Key)
		}
		seen[c.Key] = true

		if !slices.Contains(configTypes, c.Type) {
			reject("config_schema %q: type %q must be one of %s", c.Key, c.Type, strings.Join(configTypes, ", "))
			continue
		}
		if c.Type == "duration" {
			if s, ok := c.Default.(string); c.Default != nil && (!ok || !validDuration(s)) {
				reject("config_schema %q: default %v must be a Go duration string", c.Key, c.Default)
			}
		}
		if err := validateConfigBound(c.Type, c.Min); err != nil {
			reject("config_schema %q: min %v %v", c.Key, c.Min, err)
		}
		if err := validateConfigBound(c.Type, c.Max); err != nil {
			reject("config_schema %q: max %v %v", c.Key, c.Max, err)
		}
	}

	uses := make(map[string]bool, len(m.UsesConfig))
	for _, ref := range m.UsesConfig {
		owner, key, ok := strings.Cut(ref.Key, ".")
		switch {
		case !ok || owner == "" || key == "" || strings.Contains(key, "."):
			reject("uses_config: %q must be {module}.{key}", ref.Key)
		case owner == m.Name:
			reject("uses_config: %q is this module's own key; own keys need no declaration", ref.Key)
		case !slices.Contains(m.DependsOn, owner) && !slices.Contains(m.SoftDependsOn, owner):
			reject("uses_config: %q targets module %q, which is not in depends_on or soft_depends_on", ref.Key, owner)
		case uses[ref.Key]:
			reject("uses_config: %q listed more than once", ref.Key)
		}
		uses[ref.Key] = true

		if !slices.Contains(configTypes, ref.Type) {
			reject("uses_config %q: type %q must be one of %s", ref.Key, ref.Type, strings.Join(configTypes, ", "))
		}
	}

	if len(violations) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(violations, "; "))
}

func validDuration(s string) bool {
	_, err := time.ParseDuration(s)
	return err == nil
}

// validateConfigBound checks a min or max value against the entry type: a
// JSON number for integer and float, a duration string for duration, absent
// otherwise.
func validateConfigBound(entryType string, bound any) error {
	if bound == nil {
		return nil
	}
	switch entryType {
	case "integer", "float":
		if _, ok := bound.(float64); !ok {
			return fmt.Errorf("must be a number for type %q", entryType)
		}
	case "duration":
		if s, ok := bound.(string); !ok || !validDuration(s) {
			return fmt.Errorf("must be a Go duration string for type %q", entryType)
		}
	default:
		return fmt.Errorf("is not valid for type %q", entryType)
	}
	return nil
}
