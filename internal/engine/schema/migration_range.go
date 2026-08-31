package schema

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/Masterminds/semver/v3"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// boundaryVersionPattern extracts the version literal embedded in a
// ToVersion constraint (e.g. ">= 1.4.0" -> "1.4.0") — manifest-spec.md's
// own DataMigration example always pairs FromVersion/ToVersion as a single
// version-boundary crossing, not an arbitrary compound range, so this is
// the version each migration is ordered by.
var boundaryVersionPattern = regexp.MustCompile(`\d+\.\d+\.\d+`)

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

	// SliceStable, not Slice: migration-guide.md §4 "Execution order"
	// guarantees multiple handlers sharing the same ToVersion boundary run
	// in declaration order — Slice's equal-element ordering isn't
	// guaranteed to preserve that once len(candidates) crosses Go's
	// small-slice insertion-sort threshold.
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].boundary.LessThan(candidates[j].boundary)
	})

	applicable := make([]model.DataMigration, len(candidates))
	for i, c := range candidates {
		applicable[i] = c.migration
	}
	return applicable, nil
}

// MigrationBoundaryVersion extracts the concrete version literal embedded
// in a ToVersion constraint (e.g. ">= 1.4.0" -> "1.4.0") — used both to
// order applicable migrations here and, once a migration's job succeeds,
// as the plain semver value schema.SchemaSyncPool.AdvanceDataMigrationVersion
// records as the new data_migration_version watermark.
func MigrationBoundaryVersion(toVersion string) (*semver.Version, error) {
	match := boundaryVersionPattern.FindString(toVersion)
	if match == "" {
		return nil, fmt.Errorf("could not extract a version boundary from to_version %q", toVersion)
	}
	return semver.NewVersion(match)
}
