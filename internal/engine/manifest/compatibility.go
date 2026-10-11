package manifest

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
)

func ValidateEngineVersion(m Manifest, running string) error {
	constraint, err := semver.NewConstraint(m.Engine)
	if err != nil {
		return fmt.Errorf("engine: invalid range %q: %w", m.Engine, err)
	}

	if running == "dev" {
		return nil
	}

	version, err := semver.StrictNewVersion(running)
	if err != nil {
		return fmt.Errorf("engine: running build version %q is not valid semver: %w", running, err)
	}

	if !constraint.Check(version) {
		return fmt.Errorf("engine: range %q excludes running engine version %s", m.Engine, running)
	}

	return nil
}
