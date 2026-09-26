package wasm

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
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

// applyORMStatementTimeout sets statement_timeout for the rest of tx's
// lifetime. A separate statement from applyTenantScope's own, safely so:
// tx is already open by the time either runs, and Go's database/sql pins
// one physical connection to an open *sql.Tx regardless of PgBouncer's
// pooling mode.
func applyORMStatementTimeout(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext) error {
	ms := modCtx.ormStatementTimeout().Milliseconds()
	_, err := tx.ExecContext(ctx, `SELECT set_config('statement_timeout', $1, true)`, strconv.FormatInt(ms, 10))
	return err
}

// applyTenantScopeAsTenantRole is applyTenantScope for a transaction that
// runs only module SQL, so it switches to the tenant role for good.
func applyTenantScopeAsTenantRole(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext) error {
	_, err := tx.ExecContext(ctx, `SELECT set_config('search_path', $1, true),
		set_config('app.current_user_id', $2, true),
		set_config('app.current_user_contact_id', $3, true),
		set_config('app.current_user_roles', $4, true),
		set_config('role', $5, true)`,
		"tenant_"+modCtx.TenantSlug+", public",
		modCtx.UserID, modCtx.ContactID, strings.Join(modCtx.Roles, ","),
		"tenant_"+modCtx.TenantSlug,
	)
	return err
}

// withTenantRole runs fn's module SQL as the tenant role, then switches tx
// back to its login role, even when fn fails (data-layer.md §2.2).
func withTenantRole(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, fn func() *abiv1.HostError) *abiv1.HostError {
	if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE "+tenantschema.Name(modCtx.TenantSlug)); err != nil {
		return roleSwitchError("switch to tenant role", err)
	}
	hostErr := fn()
	_, resetErr := tx.ExecContext(context.WithoutCancel(ctx), "SET LOCAL ROLE NONE")
	if hostErr != nil {
		return hostErr
	}
	if resetErr != nil {
		return roleSwitchError("reset tenant role", resetErr)
	}
	return nil
}

func roleSwitchError(op string, err error) *abiv1.HostError {
	if errors.Is(err, context.DeadlineExceeded) {
		return &abiv1.HostError{Code: abiv1.ErrCodeDBTimeout, Message: op + ": timed out", Retry: true}
	}
	return &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: op + ": " + err.Error()}
}
