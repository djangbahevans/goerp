package manifest

import (
	"errors"
	"fmt"
	"slices"
)

func validateDeclarationRules(m Manifest) error {
	var violations []error

	if m.ABIVersion != "1" {
		violations = append(violations, fmt.Errorf("abi_version %q is unsupported; supported ABI is 1", m.ABIVersion))
	}

	if len(m.HTTPAllowlist) > 0 && !slices.Contains(m.Capabilities, "http.fetch") {
		violations = append(violations, errors.New("http_allowlist requires the http.fetch capability"))
	}

	workflows := make(map[string]bool)
	for _, workflow := range m.WorkflowTypes {
		if workflows[workflow.Name] {
			violations = append(violations, fmt.Errorf("workflow_types: duplicate name %q", workflow.Name))
		}
		workflows[workflow.Name] = true
	}

	return errors.Join(violations...)
}
