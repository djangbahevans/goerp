package mfa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func HasFactor(creds []*Credential) bool {
	return slices.ContainsFunc(creds, func(c *Credential) bool {
		return c.Type.IsFactor()
	})
}

// ListAccepted applies the tenant's reset barrier before selecting platform
// factors and this tenant's factors. PlatformOnly further restricts proofs
// used to remove a platform factor.
func (s *Store) ListAccepted(ctx context.Context, userID string, scope Scope) ([]*Credential, error) {
	return listAccepted(ctx, s.db, userID, scope)
}

func (s *Store) ListAcceptedTx(ctx context.Context, tx *sql.Tx, userID string, scope Scope) ([]*Credential, error) {
	return listAccepted(ctx, tx, userID, scope)
}

func tenantSchema(ctx context.Context, q db.Execer, tenantID string) (string, error) {
	var slug string
	if err := q.QueryRowContext(ctx, `SELECT slug FROM system.tenants WHERE id = $1`, tenantID).Scan(&slug); err != nil {
		return "", fmt.Errorf("resolve mfa tenant: %w", err)
	}

	return tenantschema.Name(slug), nil
}

type scopeQuerier interface {
	db.Execer
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listAccepted(ctx context.Context, q scopeQuerier, userID string, scope Scope) ([]*Credential, error) {
	schema, err := tenantSchema(ctx, q, scope.TenantID)
	if err != nil {
		return nil, err
	}

	rows, err := q.QueryContext(ctx, `SELECT `+userMFAColumns+` FROM system.user_mfa
		WHERE user_id = $1 AND revoked_at IS NULL
		  AND (tenant_id IS NULL OR (tenant_id = $2 AND NOT $3))
		  AND EXISTS (SELECT 1 FROM `+schema+`.tenant_members
		              WHERE user_id = $1 AND status = 'active' AND mfa_reset_at IS NULL)
		ORDER BY created_at`, userID, scope.TenantID, scope.PlatformOnly)
	if err != nil {
		return nil, fmt.Errorf("list accepted mfa credentials: %w", err)
	}

	defer func() {
		_ = rows.Close()
	}()
	var creds []*Credential
	for rows.Next() {
		cred, err := scanCredential(rows)
		if err != nil {
			return nil, fmt.Errorf("scan accepted mfa credential: %w", err)
		}

		creds = append(creds, cred)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accepted mfa credentials: %w", err)
	}

	return creds, nil
}

// EnrollmentTenantTx derives scope from the session's stored proof, rather
// than its token's AMR. LockUserTx serializes the account-wide factor check.
func (s *Store) EnrollmentTenantTx(ctx context.Context, tx *sql.Tx, userID, tenantID, sessionID string) (*string, error) {
	schema, err := tenantSchema(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}

	var platformProof, hasFactors, resetting bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM system.user_mfa c
		               WHERE c.id = ss.mfa_credential_id AND c.user_id = ss.user_id
		                 AND c.tenant_id IS NULL AND ss.mfa_verified_at IS NOT NULL),
		       EXISTS (SELECT 1 FROM system.user_mfa
		               WHERE user_id = $1 AND type IN ('totp', 'webauthn') AND revoked_at IS NULL),
		       EXISTS (SELECT 1 FROM `+schema+`.tenant_members
		               WHERE user_id = $1 AND mfa_reset_at IS NOT NULL)
		FROM system.sessions ss
		WHERE ss.id = $3 AND ss.user_id = $1 AND ss.tenant_id = $2
		  AND ss.revoked_at IS NULL AND ss.expires_at > NOW()
		FOR UPDATE OF ss`, userID, tenantID, sessionID).Scan(&platformProof, &hasFactors, &resetting)
	if err != nil {
		return nil, fmt.Errorf("read mfa enrollment proof: %w", err)
	}

	if !resetting && (platformProof || !hasFactors) {
		return nil, nil
	}

	return new(tenantID), nil
}

func (s *Store) SetResetTx(ctx context.Context, tx *sql.Tx, userID, tenantID string, reset bool) error {
	schema, err := tenantSchema(ctx, tx, tenantID)
	if err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, `UPDATE `+schema+`.tenant_members
		SET mfa_reset_at = CASE WHEN $2 THEN NOW() ELSE NULL END WHERE user_id = $1`, userID, reset)
	if err != nil {
		return fmt.Errorf("set member mfa reset: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count member mfa reset: %w", err)
	}

	if n == 0 {
		return errors.New("mfa reset member not found")
	}

	return nil
}

func (s *Store) RevokeTenantTx(ctx context.Context, tx *sql.Tx, userID, tenantID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE system.user_mfa SET revoked_at = NOW()
		WHERE user_id = $1 AND tenant_id = $2 AND revoked_at IS NULL`, userID, tenantID)
	if err != nil {
		return fmt.Errorf("revoke tenant mfa credentials: %w", err)
	}

	return nil
}

func (s *Store) RevokeRecoveryCodesTx(ctx context.Context, tx *sql.Tx, userID string, tenantID *string) error {
	_, err := tx.ExecContext(ctx, `UPDATE system.user_mfa SET revoked_at = NOW()
		WHERE user_id = $1 AND type = 'recovery_code' AND tenant_id IS NOT DISTINCT FROM $2::uuid
		  AND revoked_at IS NULL`, userID, tenantID)
	if err != nil {
		return fmt.Errorf("revoke scoped recovery codes: %w", err)
	}

	return nil
}

func (s *Store) HasRecoveryCodesTx(ctx context.Context, tx *sql.Tx, userID string, tenantID *string) (bool, error) {
	var found bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM system.user_mfa
 WHERE user_id = $1 AND tenant_id IS NOT DISTINCT FROM $2::uuid
 AND type = 'recovery_code' AND revoked_at IS NULL)`, userID, tenantID).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("check scoped recovery codes: %w", err)
	}

	return found, nil
}
