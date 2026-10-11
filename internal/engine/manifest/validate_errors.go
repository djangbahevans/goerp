package manifest

import (
	"fmt"
	"strings"
)

func validateErrorCodes(m Manifest) error {
	for i, code := range m.ErrorCodes {
		if !validEventNameRegex.MatchString(code) {
			return fmt.Errorf("error_codes[%d]: %q must be module.resource.condition with lowercase alphanumeric/underscore segments starting with a letter", i, code)
		}

		owner, _, _ := strings.Cut(code, ".")
		if owner != m.Name {
			return fmt.Errorf("error_codes[%d]: %q belongs to module %q, not declaring module %q", i, code, owner, m.Name)
		}
	}

	return nil
}
