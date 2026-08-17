package schema

import (
	"context"
	"fmt"

	"ariga.io/atlas/sql/schema"
	"github.com/Masterminds/semver/v3"
	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// DowngradeStatus is the outcome of CheckDowngrade.
type DowngradeStatus int

const (
	// DowngradeStatusNone: newVersion isn't older than currentVersion.
	DowngradeStatusNone DowngradeStatus = iota
	// DowngradeStatusSupersetSafe: live only has extras newVersion doesn't declare.
	DowngradeStatusSupersetSafe
	// DowngradeStatusBlocked: live has something newVersion's code can't operate against.
	DowngradeStatusBlocked
)

func (s DowngradeStatus) String() string {
	switch s {
	case DowngradeStatusNone:
		return "none"
	case DowngradeStatusSupersetSafe:
		return "superset_safe"
	case DowngradeStatusBlocked:
		return "blocked"
	default:
		return "unknown_status"
	}
}

// CheckDowngrade diffs newVersion's own declared schema (targetModelDecls,
// its get_model_declarations output) against the live database schema.
// newVersion >= currentVersion isn't a downgrade and short-circuits to
// DowngradeStatusNone.
func (e *SchemaDiffEngine) CheckDowngrade(ctx context.Context, sess *SchemaSyncSession, currentVersion, newVersion string, targetModelDecls []model.ModelDeclaration) (DowngradeStatus, []string, error) {
	current, err := semver.NewVersion(currentVersion)
	if err != nil {
		return DowngradeStatusNone, nil, fmt.Errorf("parse current version %q: %w", currentVersion, err)
	}
	target, err := semver.NewVersion(newVersion)
	if err != nil {
		return DowngradeStatusNone, nil, fmt.Errorf("parse new version %q: %w", newVersion, err)
	}
	if !target.LessThan(current) {
		return DowngradeStatusNone, nil, nil
	}

	changes, err := e.Diff(ctx, sess, targetModelDecls)
	if err != nil {
		return DowngradeStatusNone, nil, err
	}

	if blocked := incompatibleDowngradeChanges(changes); len(blocked) > 0 {
		return DowngradeStatusBlocked, blocked, nil
	}

	log.Warn().
		Str("tenant", sess.tenantSlug).
		Str("module", sess.moduleName).
		Str("current_version", currentVersion).
		Str("new_version", newVersion).
		Msg("downgrade proceeding against a superset live schema")

	return DowngradeStatusSupersetSafe, nil, nil
}

// incompatibleDowngradeChanges names changes in a live-vs-target diff that
// newVersion's code can't safely operate against: AddTable/AddColumn mean
// something the target expects is missing from live; DropColumn is safe
// only if that extra live column is nullable or has a default (else the
// target's INSERTs, which never set it, would violate NOT NULL). Extra
// tables/indexes live has beyond the target's declaration are inert to code
// that never references them.
func incompatibleDowngradeChanges(changes []schema.Change) []string {
	var blocked []string

	for _, tc := range explodeChanges(changes) {
		switch c := tc.change.(type) {
		case *schema.AddTable:
			blocked = append(blocked, fmt.Sprintf("table %q is missing from the live schema", c.T.Name))
		case *schema.AddColumn:
			blocked = append(blocked, fmt.Sprintf("column %q.%q is missing from the live schema", tc.table.Name, c.C.Name))
		case *schema.DropColumn:
			if !c.C.Type.Null && c.C.Default == nil {
				blocked = append(blocked, fmt.Sprintf("column %q.%q is NOT NULL with no default; the downgraded code won't supply it on INSERT — add a default or backfill before downgrading", tc.table.Name, c.C.Name))
			}
		case *schema.ModifyColumn:
			if !isSafeModifyColumn(c) {
				blocked = append(blocked, fmt.Sprintf("column %q.%q changed in a way the downgraded code can't safely read or write", tc.table.Name, c.To.Name))
			}
		case *schema.RenameColumn:
			blocked = append(blocked, fmt.Sprintf("column %q renamed to %q, which the downgraded code doesn't expect", c.From.Name, c.To.Name))
		case *schema.RenameTable:
			blocked = append(blocked, fmt.Sprintf("table %q renamed to %q, which the downgraded code doesn't expect", c.From.Name, c.To.Name))
		}
	}

	return blocked
}
