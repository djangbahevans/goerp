package loader

import (
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/rs/zerolog/log"
)

// ValidateUsesConfig resolves every module's uses_config references
// (manifest-spec.md §17) against the owning module's config_schema and
// records the result on LoadedModule.UsesConfig for host.config.get.
//
//   - An owner that is loaded must declare the key with the type the
//     reference expects, or the referencing module fails to load.
//   - An owner that is not loaded and is a soft dependency leaves the
//     reference unresolved with a warning; reads of it return no value.
//   - An owner that is not loaded and is not a soft dependency fails the
//     module. A hard dependency that failed would already have cascaded
//     the failure, so this is guarded defensively.
//
// Exported so a caller loading modules one at a time (not via LoadAll) can
// run the same validation once its own loop finishes, the same pattern as
// ValidateEventSubscriptions.
func ValidateUsesConfig(modules map[string]*module.LoadedModule) {
	// Owners are judged by their status on entry, so a module this pass
	// fails does not change the outcome for modules visited after it.
	usable := make(map[string]bool, len(modules))
	for name, m := range modules {
		usable[name] = m.Status != module.StatusFailed
	}

	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}

		resolved := make(map[string]manifest.UsesConfigEntry, len(m.Manifest.UsesConfig))
		for _, ref := range m.Manifest.UsesConfig {
			ownerName, key, _ := strings.Cut(ref.Key, ".")

			owner := modules[ownerName]
			if !usable[ownerName] {
				if !slices.Contains(m.Manifest.SoftDependsOn, ownerName) {
					m.Fail(fmt.Sprintf("uses_config: %q targets module %q, which is not loaded", ref.Key, ownerName))
					break
				}
				log.Warn().Str("module", name).Str("config_key", ref.Key).
					Msg("uses_config key is owned by a soft dependency that is not loaded; reads return no value")
				resolved[ref.Key] = manifest.UsesConfigEntry{}
				continue
			}

			entry, found := findConfigEntry(owner.Manifest.ConfigSchema, key)
			switch {
			case !found:
				m.Fail(fmt.Sprintf("uses_config: %q: module %q declares no config key %q", ref.Key, ownerName, key))
			case entry.Type != ref.Type:
				m.Fail(fmt.Sprintf("uses_config: %q is expected as type %q but module %q declares it as %q", ref.Key, ref.Type, ownerName, entry.Type))
			default:
				resolved[ref.Key] = manifest.UsesConfigEntry{Entry: entry, Loaded: true}
			}
			if m.Status == module.StatusFailed {
				break
			}
		}

		if m.Status != module.StatusFailed {
			m.UsesConfig = resolved
		}
	}
}

func findConfigEntry(schema []manifest.ConfigEntry, key string) (manifest.ConfigEntry, bool) {
	i := slices.IndexFunc(schema, func(c manifest.ConfigEntry) bool { return c.Key == key })
	if i < 0 {
		return manifest.ConfigEntry{}, false
	}
	return schema[i], true
}
