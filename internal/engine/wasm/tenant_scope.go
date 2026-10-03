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

func (mc *ModuleContext) database(primary *sql.DB) (*sql.DB, error) {
	if !mc.IsDataMigrationJob {
		return primary, nil
	}

	if mc.dataMigrationDB == nil {
		return nil, errors.New("data migration requires the schema-sync database pool")
	}

	return mc.dataMigrationDB, nil
}

// Tenant and ABAC settings are transaction-local so pooled connections cannot
// carry them into another invocation, including migration transactions.
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

func applyORMStatementTimeout(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext) error {
	ms := modCtx.ormStatementTimeout().Milliseconds()
	_, err := tx.ExecContext(ctx, `SELECT set_config('statement_timeout', $1, true)`, strconv.FormatInt(ms, 10))
	return err
}

// Standalone module queries use the tenant role; migrations retain BYPASSRLS.
func applyTenantScopeAsTenantRole(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext) error {
	if modCtx.IsDataMigrationJob {
		return applyTenantScope(ctx, tx, modCtx)
	}

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

// Ordinary module SQL temporarily uses the tenant role so engine bookkeeping
// can resume as the login role. Migration SQL retains BYPASSRLS.
func withTenantRole(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, fn func() *abiv1.HostError) *abiv1.HostError {
	if modCtx.IsDataMigrationJob {
		return fn()
	}

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
