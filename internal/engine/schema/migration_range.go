package schema

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/Masterminds/semver/v3"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

var boundaryVersionPattern = regexp.MustCompile(`\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?`)

// ApplicableDataMigrations returns the subset of migrations that apply to
// a tenant upgrading a module from currentVersion to targetVersion — each
// migration whose FromVersion constraint matches currentVersion and whose
// ToVersion constraint matches targetVersion — ordered by the version
// boundary each migration's ToVersion names, regardless of declaration
// order, for correct sequential application.
func ApplicableDataMigrations(currentVersion, targetVersion string, migrations []model.DataMigration) ([]model.DataMigration, error) {
	current, err := semver.NewVersion(currentVersion)
	if err != nil {
		return nil, fmt.Errorf("parse current version %q: %w", currentVersion, err)
	}
	target, err := semver.NewVersion(targetVersion)
	if err != nil {
		return nil, fmt.Errorf("parse target version %q: %w", targetVersion, err)
	}

	type candidate struct {
		migration model.DataMigration
		boundary  *semver.Version
	}

	var candidates []candidate
	for _, m := range migrations {
		fromConstraint, err := semver.NewConstraint(m.FromVersion)
		if err != nil {
			return nil, fmt.Errorf("migration %q: parse from_version %q: %w", m.Handler, m.FromVersion, err)
		}
		toConstraint, err := semver.NewConstraint(m.ToVersion)
		if err != nil {
			return nil, fmt.Errorf("migration %q: parse to_version %q: %w", m.Handler, m.ToVersion, err)
		}

		if !fromConstraint.Check(current) || !toConstraint.Check(target) {
			continue
		}

		boundary, err := MigrationBoundaryVersion(m.ToVersion)
		if err != nil {
			return nil, fmt.Errorf("migration %q: %w", m.Handler, err)
		}
		candidates = append(candidates, candidate{migration: m, boundary: boundary})
	}

	// Stable: handlers sharing a ToVersion boundary run in declaration order.
	slices.SortStableFunc(candidates, func(a, b candidate) int { return a.boundary.Compare(b.boundary) })

	applicable := make([]model.DataMigration, len(candidates))
	for i, c := range candidates {
		applicable[i] = c.migration
	}
	return applicable, nil
}

// Migration targets name a single boundary, including prerelease and build suffixes.
func MigrationBoundaryVersion(toVersion string) (*semver.Version, error) {
	match := boundaryVersionPattern.FindString(toVersion)
	if match == "" {
		return nil, fmt.Errorf("could not extract a version boundary from to_version %q", toVersion)
	}
	return semver.NewVersion(match)
}
