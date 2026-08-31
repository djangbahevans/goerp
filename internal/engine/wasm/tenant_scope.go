package wasm

import (
	"context"
	"database/sql"
	"strings"
)

// applyTenantScope sets tx's search_path to modCtx's tenant schema, plus
// the ABAC session variables Layer 2a (multitenancy-internals.md §5a)
// reads off it, as one SET LOCAL statement — the Layer 1 tenant-isolation
// mechanism (multitenancy-internals.md §5) every host.db/host.orm
// transaction goes through before running any module-triggered SQL.
//
// Issued as a single `SELECT set_config(...), set_config(...), ...`
// statement, not four separate ones: PgBouncer's transaction-pooling mode
// only pins a backend connection to tx for the duration of this one open
// transaction, so a gap between separate autocommit-style statements
// could let a later statement land on a different backend with no
// search_path/ABAC context set at all (multitenancy-internals.md §5's own
// "PgBouncer correctness note"). tx is always already open when this is
// called, so there is no such gap here regardless — this shape only
// matters if a caller is ever tempted to split it back out.
func applyTenantScope(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext) error {
	_, err := tx.ExecContext(ctx, `SELECT set_config('search_path', $1, true),
		set_config('app.current_user_id', $2, true),
		set_config('app.current_user_contact_id', $3, true),
		set_config('app.current_user_roles', $4, true)`,
		"tenant_"+modCtx.TenantSlug+", public",
		modCtx.UserID, modCtx.ContactID, strings.Join(modCtx.Roles, ","),
	)
	return err
}
