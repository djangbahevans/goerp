package schema

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func IsQueuedWorkCompatible(migrations []model.DataMigration, fromVersion, toVersion string) (bool, error) {
	if fromVersion == "" || fromVersion == toVersion {
		return true, nil
	}

	from, err := semver.NewVersion(fromVersion)
	if err != nil {
		return false, fmt.Errorf("parse pinned module version %q: %w", fromVersion, err)
	}

	to, err := semver.NewVersion(toVersion)
	if err != nil {
		return false, fmt.Errorf("parse live module version %q: %w", toVersion, err)
	}

	for _, migration := range migrations {
		boundary, err := MigrationBoundaryVersion(migration.ToVersion)
		if err != nil {
			return false, fmt.Errorf("migration %q: %w", migration.Handler, err)
		}

		if boundary.GreaterThan(from) && !boundary.GreaterThan(to) {
			return false, nil
		}
	}

	return true, nil
}
