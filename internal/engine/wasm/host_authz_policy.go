package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/djangbahevans/goerp/internal/engine/policy"
	"github.com/jackc/pgx/v5/pgconn"
)

// invalidTextRepresentation is Postgres's SQLSTATE for a value that is not
// valid text for its column type, such as a malformed UUID primary key.
const invalidTextRepresentation = "22P02"

// evaluateRecordPolicies reports whether the policies that scope a permission
// admit the record named by resourceID for modCtx's caller. It combines them
// as the RLS policies on the table do: the OR of the permissive policies,
// ANDed with every restrictive one, so with no permissive policy nothing is
// admitted. Every policy is evaluated, so a policy that cannot be evaluated is
// an error whatever the others say, never an allow.
func evaluateRecordPolicies(ctx context.Context, db *sql.DB, modCtx *ModuleContext, policies []policy.Policy, resourceID string) (allowed bool, reason string, hostErr *abiv1.HostError) {
	for _, p := range policies {
		if p.Unresolved != nil {
			return false, "", policyEvaluationError(p.Name, p.Unresolved)
		}
	}

	record, hostErr := fetchPolicyRecord(ctx, db, modCtx, policies, resourceID)
	if hostErr != nil {
		return false, "", hostErr
	}

	env := domain.Env{
		Record:    record,
		UserID:    modCtx.UserID,
		ContactID: modCtx.ContactID,
		TenantID:  modCtx.TenantID,
		Roles:     modCtx.Roles,
		HasPermission: func(name string) bool {
			return callerHasPermission(modCtx, modCtx.PermissionRegistry(), name)
		},
	}
	var permissive, admitting int
	var rejectedBy string
	for _, p := range policies {
		ok, err := domain.Eval(p.Expr, env)
		if err != nil {
			return false, "", policyEvaluationError(p.Name, err)
		}
		switch {
		case p.Restrictive && !ok && rejectedBy == "":
			rejectedBy = p.Name
		case !p.Restrictive:
			permissive++
			if ok {
				admitting++
			}
		}
	}

	switch {
	case rejectedBy != "":
		return false, fmt.Sprintf("record is not admitted by restrictive policy %q", rejectedBy), nil
	case permissive == 0:
		return false, "no permissive policy scopes this permission, so no record is admitted", nil
	case admitting == 0 && permissive == 1:
		return false, fmt.Sprintf("record is not admitted by policy %q", firstPermissive(policies)), nil
	case admitting == 0:
		return false, fmt.Sprintf("record is not admitted by any of the %d permissive policies that scope this permission", permissive), nil
	}
	return true, "", nil
}

// fetchPolicyRecord reads the columns the policies reference from the record
// with primary key resourceID. It reads through the schema-sync pool, which
// bypasses RLS, because the policies are evaluated here and a row they hide
// from the caller must still be found to be judged. A missing row is
// authz.resource_not_found.
func fetchPolicyRecord(ctx context.Context, db *sql.DB, modCtx *ModuleContext, policies []policy.Policy, resourceID string) (map[string]any, *abiv1.HostError) {
	table, pk := policies[0].Table, policies[0].PKColumn

	columnSet := map[string]struct{}{pk: {}}
	for _, p := range policies {
		for _, c := range p.Columns {
			columnSet[c] = struct{}{}
		}
	}
	columns := slices.Sorted(maps.Keys(columnSet))

	quoted := make([]string, len(columns))
	for i, c := range columns {
		quoted[i] = quoteIdentifier(c)
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s = $1", strings.Join(quoted, ", "), quoteIdentifier(table), quoteIdentifier(pk))

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	defer func() { _ = tx.Rollback() }()

	if err := applyTenantScope(ctx, tx, modCtx); err != nil {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	if err := applyORMStatementTimeout(ctx, tx, modCtx); err != nil {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	values := make([]any, len(columns))
	targets := make([]any, len(columns))
	for i := range values {
		targets[i] = &values[i]
	}
	if err := tx.QueryRowContext(ctx, query, resourceID).Scan(targets...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, resourceNotFound(resourceID)
		}
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == invalidTextRepresentation {
			return nil, resourceNotFound(resourceID)
		}
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	record := make(map[string]any, len(columns))
	for i, c := range columns {
		record[c] = recordValue(values[i])
	}
	return record, nil
}

func firstPermissive(policies []policy.Policy) string {
	for _, p := range policies {
		if !p.Restrictive {
			return p.Name
		}
	}
	return ""
}

func resourceNotFound(resourceID string) *abiv1.HostError {
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeAuthzResourceNotFound,
		Message: fmt.Sprintf("no record %q to evaluate policies against", resourceID),
	}
}

// recordValue turns a driver value into the form domain.Eval compares:
// byte slices (text-encoded numerics and the like) become strings and a
// 16-byte UUID array its canonical text.
func recordValue(v any) any {
	switch v := v.(type) {
	case []byte:
		return string(v)
	case [16]byte:
		return fmt.Sprintf("%x-%x-%x-%x-%x", v[0:4], v[4:6], v[6:8], v[8:10], v[10:16])
	default:
		return v
	}
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
