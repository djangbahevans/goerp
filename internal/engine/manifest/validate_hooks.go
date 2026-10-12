package manifest

import (
	"fmt"
	"regexp"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

var hookNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
var hookHandlerPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateHooks(m Manifest) error {
	seen := make(map[string]bool)
	for _, hook := range m.Hooks {
		if !hookNamePattern.MatchString(hook.Hook) || !hookHandlerPattern.MatchString(hook.Handler) {
			return fmt.Errorf("hooks: invalid hook %q or handler %q", hook.Hook, hook.Handler)
		}
		if strings.HasPrefix(hook.Hook, "engine.") && !abiv1.EngineHook(hook.Hook) {
			return fmt.Errorf("hooks: unknown engine hook %q", hook.Hook)
		}

		key := hook.Hook + "/" + hook.Handler
		if seen[key] {
			return fmt.Errorf("hooks: duplicate registration %q", key)
		}
		seen[key] = true
	}

	return nil
}

func (h Hook) EffectivePriority() int {
	if h.Priority == nil {
		return 50
	}

	return *h.Priority
}
