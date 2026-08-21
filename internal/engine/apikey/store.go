package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

// createAPIKeysTable matches auth-internals.md §7's api_keys schema
// exactly, schema-qualified against system (same convention
// tenant.Store's own DDL uses).
const createAPIKeysTable = `
CREATE TABLE IF NOT EXISTS system.api_keys (
    id              UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id       UUID NOT NULL REFERENCES system.tenants(id) ON DELETE CASCADE,
    user_id         UUID REFERENCES system.users(id),
    name            TEXT NOT NULL,
    key_hash        TEXT NOT NULL UNIQUE,
    key_prefix      TEXT NOT NULL,
    scopes          TEXT[] NOT NULL DEFAULT '{}',
    allowed_ips     INET[],
    expires_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    last_used_ip    INET,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by      UUID REFERENCES system.users(id),
    revoked_at      TIMESTAMPTZ,
    revoke_reason   TEXT
)
`

const createAPIKeysHashIndex = `
CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON system.api_keys(key_hash)
    WHERE revoked_at IS NULL
`

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap creates system.api_keys and its partial hash index if they
// don't already exist. Idempotent and concurrent-safe against other
// processes calling Bootstrap at the same time, same convention
// tenant.Store.Bootstrap uses (goerp#171).
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("apikey.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createAPIKeysTable); err != nil {
			return fmt.Errorf("create api_keys table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createAPIKeysHashIndex); err != nil {
			return fmt.Errorf("create api_keys hash index: %w", err)
		}
		return nil
	})
}

// generateKey returns a new key in auth-internals.md §7's
// erp_{prefix8}_{random32} format, plus the SHA-256 hex hash of the full
// key — the raw key is never persisted, only its hash, same
// generate-then-hash shape invite.generateToken already uses.
func generateKey() (fullKey, prefix, keyHash string, err error) {
	prefixBytes := make([]byte, 6)
	if _, err := rand.Read(prefixBytes); err != nil {
		return "", "", "", fmt.Errorf("generate api key prefix: %w", err)
	}
	bodyBytes := make([]byte, 24)
	if _, err := rand.Read(bodyBytes); err != nil {
		return "", "", "", fmt.Errorf("generate api key body: %w", err)
	}

	prefix = base64.RawURLEncoding.EncodeToString(prefixBytes)
	body := base64.RawURLEncoding.EncodeToString(bodyBytes)
	fullKey = fmt.Sprintf("erp_%s_%s", prefix, body)

	sum := sha256.Sum256([]byte(fullKey))
	keyHash = fmt.Sprintf("%x", sum)
	return fullKey, prefix, keyHash, nil
}

const apiKeyColumns = `id, tenant_id, user_id, name, key_hash, key_prefix, scopes::text, allowed_ips::text, expires_at, last_used_at, last_used_ip, created_at, created_by, revoked_at, revoke_reason`

type rowScanner interface {
	Scan(dest ...any) error
}

// encodePGTextArray renders elems as a Postgres array literal
// ({"a","b"}) — always double-quoting each element (Postgres accepts
// this universally) rather than implementing the full
// quote-only-when-needed grammar Postgres itself uses on output.
// Scanning scopes/allowed_ips as rich Go types (pgtype.Array[string])
// doesn't work through plain database/sql with this driver setup — the
// value round-trips as SQL NULL regardless of binding — so both columns
// are cast to/from text and handled as array-literal strings instead.
func encodePGTextArray(elems []string) string {
	if len(elems) == 0 {
		return "{}"
	}
	quoted := make([]string, len(elems))
	for i, e := range elems {
		e = strings.ReplaceAll(e, `\`, `\\`)
		e = strings.ReplaceAll(e, `"`, `\"`)
		quoted[i] = `"` + e + `"`
	}
	return "{" + strings.Join(quoted, ",") + "}"
}

// decodePGTextArray parses a Postgres array literal ({a,b} or
// {"a","b c"}) back into elements — needed because Postgres's own
// text-cast output doesn't always match what encodePGTextArray produced
// (it only quotes an element when it needs to).
func decodePGTextArray(s string) []string {
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	if s == "" {
		return []string{}
	}

	elems := []string{}
	var cur strings.Builder
	inQuotes := false
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuotes = !inQuotes
		case r == ',' && !inQuotes:
			elems = append(elems, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	elems = append(elems, cur.String())
	return elems
}

// scanAPIKey unpacks one apiKeyColumns row.
func scanAPIKey(sc rowScanner) (*APIKey, error) {
	var k APIKey
	var userID, lastUsedIP, createdBy, revokeReason sql.NullString
	var expiresAt, lastUsedAt, revokedAt sql.NullTime
	var scopesText string
	var allowedIPsText sql.NullString

	if err := sc.Scan(
		&k.ID, &k.TenantID, &userID, &k.Name, &k.KeyHash, &k.KeyPrefix,
		&scopesText, &allowedIPsText, &expiresAt, &lastUsedAt, &lastUsedIP,
		&k.CreatedAt, &createdBy, &revokedAt, &revokeReason,
	); err != nil {
		return nil, err
	}

	if userID.Valid {
		k.UserID = &userID.String
	}
	if lastUsedIP.Valid {
		k.LastUsedIP = &lastUsedIP.String
	}
	if createdBy.Valid {
		k.CreatedBy = &createdBy.String
	}
	if revokeReason.Valid {
		k.RevokeReason = &revokeReason.String
	}
	if expiresAt.Valid {
		k.ExpiresAt = &expiresAt.Time
	}
	if lastUsedAt.Valid {
		k.LastUsedAt = &lastUsedAt.Time
	}
	if revokedAt.Valid {
		k.RevokedAt = &revokedAt.Time
	}
	k.Scopes = decodePGTextArray(scopesText)
	if allowedIPsText.Valid {
		k.AllowedIPs = decodePGTextArray(allowedIPsText.String)
	}

	return &k, nil
}

// IssueKey generates and inserts a new key, returning the one-time
// plaintext (never retrievable again) alongside the stored row. userID
// nil creates a service key. scopes/allowedIPs may be nil (allowedIPs
// nil means any IP is allowed, per the schema's own column comment).
func (s *Store) IssueKey(ctx context.Context, tenantID string, userID *string, name string, scopes, allowedIPs []string, expiresAt *time.Time, createdBy *string) (fullKey string, key *APIKey, err error) {
	fullKey, prefix, keyHash, err := generateKey()
	if err != nil {
		return "", nil, err
	}

	// allowedIPs == nil binds a real SQL NULL ("any IP allowed"); a
	// non-nil (even empty) slice always encodes to a literal array,
	// matching the Go nil-vs-empty-slice distinction the AllowedIPs field
	// itself documents.
	var allowedIPsParam any
	if allowedIPs != nil {
		allowedIPsParam = encodePGTextArray(allowedIPs)
	}

	row := s.db.QueryRowContext(ctx, `
		INSERT INTO system.api_keys (tenant_id, user_id, name, key_hash, key_prefix, scopes, allowed_ips, expires_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6::text[], $7::inet[], $8, $9)
		RETURNING `+apiKeyColumns,
		tenantID, userID, name, keyHash, prefix,
		encodePGTextArray(scopes), allowedIPsParam,
		expiresAt, createdBy,
	)
	k, err := scanAPIKey(row)
	if err != nil {
		return "", nil, fmt.Errorf("insert api key: %w", err)
	}
	return fullKey, k, nil
}

// LookupByHash hashes fullKey and looks up the still-live (not revoked)
// key it belongs to. Deliberately does not check ExpiresAt — expiry is a
// distinct 401 case in auth-internals.md §7's flow (a separate error code
// from "not found"), which is goerp#223's runtime-dispatch job to check
// against the returned row.
func (s *Store) LookupByHash(ctx context.Context, fullKey string) (*APIKey, error) {
	sum := sha256.Sum256([]byte(fullKey))
	keyHash := fmt.Sprintf("%x", sum)

	row := s.db.QueryRowContext(ctx, `
		SELECT `+apiKeyColumns+`
		FROM system.api_keys
		WHERE key_hash = $1 AND revoked_at IS NULL
	`, keyHash)
	k, err := scanAPIKey(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAPIKeyNotFound
		}
		return nil, fmt.Errorf("lookup api key: %w", err)
	}
	return k, nil
}

// Revoke marks id revoked with reason. Immediate — no TTL delay,
// idx_api_keys_hash's own WHERE revoked_at IS NULL means the next
// LookupByHash for this key's hash simply stops matching.
func (s *Store) Revoke(ctx context.Context, id, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE system.api_keys SET revoked_at = NOW(), revoke_reason = $2
		WHERE id = $1
	`, id, reason)
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	return nil
}

// UpdateLastUsed sets id's last_used_at/last_used_ip. A synchronous
// method — invoking it fire-and-forget from a real request path is
// goerp#223's job, not this package's.
func (s *Store) UpdateLastUsed(ctx context.Context, id, ip string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE system.api_keys SET last_used_at = NOW(), last_used_ip = $2::inet
		WHERE id = $1
	`, id, ip)
	if err != nil {
		return fmt.Errorf("update api key last used: %w", err)
	}
	return nil
}
