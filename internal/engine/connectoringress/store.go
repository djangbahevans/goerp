// Package connectoringress owns the system tables behind connector webhook
// ingress (connector-guide.md §3 "Inbound webhooks"): system.connector_webhook_endpoints
// maps an opaque URL token to a tenant and connector module before any tenant
// context exists, and system.connector_inbox durably and idempotently records
// each accepted delivery.
package connectoringress

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

// ErrEndpointNotFound is returned when no active endpoint matches: the token
// is unknown, revoked, or belongs to a different module.
var ErrEndpointNotFound = errors.New("webhook endpoint not found")

// tokenBytes is the entropy of a webhook token (multitenancy-internals.md §2).
const tokenBytes = 32

// tokenLength is the base62 length that holds tokenBytes of entropy: 62^43
// exceeds 2^256 and 62^42 does not.
const tokenLength = 43

// createConnectorWebhookEndpointsTable matches multitenancy-internals.md §2.
const createConnectorWebhookEndpointsTable = `
CREATE TABLE IF NOT EXISTS system.connector_webhook_endpoints (
    token       TEXT PRIMARY KEY,
    tenant_id   UUID NOT NULL REFERENCES system.tenants(id) ON DELETE CASCADE,
    module_name TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at  TIMESTAMPTZ,
    UNIQUE (tenant_id, module_name)
)
`

// The unique constraint is the idempotency key: a redelivery of the same
// provider event conflicts and inserts nothing, before any response is sent.
const createConnectorInboxTable = `
CREATE TABLE IF NOT EXISTS system.connector_inbox (
    id                UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id         UUID NOT NULL REFERENCES system.tenants(id) ON DELETE CASCADE,
    module_name       TEXT NOT NULL,
    provider_event_id TEXT NOT NULL,
    payload           JSONB NOT NULL,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status            TEXT NOT NULL DEFAULT 'pending'
                          CHECK (status IN ('pending', 'processed', 'failed')),
    processed_at      TIMESTAMPTZ,
    UNIQUE (tenant_id, module_name, provider_event_id)
)
`

// Store reads and writes the connector webhook ingress tables.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap creates both tables if they don't already exist. Idempotent and
// safe against concurrent callers via db.WithAdvisoryLock.
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("connectoringress.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createConnectorWebhookEndpointsTable); err != nil {
			return fmt.Errorf("create connector_webhook_endpoints table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createConnectorInboxTable); err != nil {
			return fmt.Errorf("create connector_inbox table: %w", err)
		}
		return nil
	})
}

// newToken returns tokenBytes of crypto/rand entropy as a zero-padded base62
// string of exactly tokenLength characters.
func newToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return encodeToken(raw), nil
}

func encodeToken(raw []byte) string {
	digits := new(big.Int).SetBytes(raw).Text(62)
	return strings.Repeat("0", tokenLength-len(digits)) + digits
}

// MintEndpoint returns the active webhook token for tenantID and moduleName,
// creating one when none exists. A revoked endpoint is replaced by a new
// token. The statement is atomic, so concurrent callers all receive the same
// token.
func (s *Store) MintEndpoint(ctx context.Context, tenantID, moduleName string) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", fmt.Errorf("generate webhook token: %w", err)
	}

	var minted string
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO system.connector_webhook_endpoints AS e (token, tenant_id, module_name)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, module_name) DO UPDATE SET
			token      = CASE WHEN e.revoked_at IS NULL THEN e.token ELSE EXCLUDED.token END,
			created_at = CASE WHEN e.revoked_at IS NULL THEN e.created_at ELSE NOW() END,
			revoked_at = NULL
		RETURNING token
	`, token, tenantID, moduleName).Scan(&minted)
	if err != nil {
		return "", fmt.Errorf("mint webhook endpoint for tenant %s module %s: %w", tenantID, moduleName, err)
	}
	return minted, nil
}

// RevokeEndpoint revokes the active endpoint of tenantID and moduleName, after
// which ResolveEndpoint no longer matches its token. It returns
// ErrEndpointNotFound when there is no active endpoint to revoke.
func (s *Store) RevokeEndpoint(ctx context.Context, tenantID, moduleName string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE system.connector_webhook_endpoints
		SET revoked_at = NOW()
		WHERE tenant_id = $1 AND module_name = $2 AND revoked_at IS NULL
	`, tenantID, moduleName)
	if err != nil {
		return fmt.Errorf("revoke webhook endpoint for tenant %s module %s: %w", tenantID, moduleName, err)
	}
	revoked, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count revoked webhook endpoints: %w", err)
	}
	if revoked == 0 {
		return ErrEndpointNotFound
	}
	return nil
}

// ResolveEndpoint returns the tenant ID the active endpoint token belongs to
// for moduleName. It needs no tenant context. A token that is unknown,
// revoked or issued for another module returns ErrEndpointNotFound.
func (s *Store) ResolveEndpoint(ctx context.Context, token, moduleName string) (string, error) {
	var tenantID string
	err := s.db.QueryRowContext(ctx, `
		SELECT tenant_id
		FROM system.connector_webhook_endpoints
		WHERE token = $1 AND module_name = $2 AND revoked_at IS NULL
	`, token, moduleName).Scan(&tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrEndpointNotFound
	}
	if err != nil {
		return "", fmt.Errorf("resolve webhook endpoint: %w", err)
	}
	return tenantID, nil
}

// InsertInbox records an accepted delivery. payload must be a valid JSON
// document. It reports inserted=false, with an empty id, when the provider
// event was already recorded for the tenant and module; the duplicate check
// and the insert are one atomic statement.
func (s *Store) InsertInbox(ctx context.Context, tenantID, moduleName, providerEventID string, payload []byte) (id string, inserted bool, err error) {
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO system.connector_inbox (tenant_id, module_name, provider_event_id, payload)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (tenant_id, module_name, provider_event_id) DO NOTHING
		RETURNING id
	`, tenantID, moduleName, providerEventID, string(payload)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("insert connector inbox row for tenant %s module %s: %w", tenantID, moduleName, err)
	}
	return id, true, nil
}
