package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Family is one login's session family, presented through its live row
// (auth-internals.md §4 "Session management endpoints").
type Family struct {
	ID           string
	LiveRowID    string
	UserAgent    *string
	IPAddress    *string
	CountryCode  *string
	SignedInAt   time.Time
	LastActiveAt time.Time
	Persistent   bool
}

const liveFamiliesQuery = `
	SELECT l.family_id, l.id, l.user_agent, host(l.ip_address), l.country_code,
	       (SELECT MIN(f.created_at) FROM system.sessions f WHERE f.family_id = l.family_id),
	       l.last_active_at, l.persistent
	FROM system.sessions l
	WHERE l.user_id = $1 AND l.tenant_id = $2
	  AND l.revoked_at IS NULL AND l.rotated_at IS NULL AND l.expires_at > NOW()
`

func scanFamily(sc interface{ Scan(dest ...any) error }) (Family, error) {
	var f Family
	err := sc.Scan(&f.ID, &f.LiveRowID, &f.UserAgent, &f.IPAddress, &f.CountryCode, &f.SignedInAt, &f.LastActiveAt, &f.Persistent)
	return f, err
}

// LiveFamiliesForUserInTenant returns userID's live session families in
// tenantID, most recently active first.
func (s *Store) LiveFamiliesForUserInTenant(ctx context.Context, userID, tenantID string) ([]Family, error) {
	rows, err := s.db.QueryContext(ctx, liveFamiliesQuery+` ORDER BY l.last_active_at DESC, l.family_id`, userID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query live session families: %w", err)
	}
	defer func() { _ = rows.Close() }()

	families := []Family{}
	for rows.Next() {
		f, err := scanFamily(rows)
		if err != nil {
			return nil, fmt.Errorf("scan session family: %w", err)
		}
		families = append(families, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session families: %w", err)
	}
	return families, nil
}

// LiveFamilyForUserInTenant returns familyID when it is a live family of
// userID in tenantID, and ErrSessionNotFound otherwise. familyID must be a
// well-formed UUID.
func (s *Store) LiveFamilyForUserInTenant(ctx context.Context, userID, tenantID, familyID string) (Family, error) {
	f, err := scanFamily(s.db.QueryRowContext(ctx, liveFamiliesQuery+` AND l.family_id = $3`, userID, tenantID, familyID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Family{}, ErrSessionNotFound
		}
		return Family{}, fmt.Errorf("get live session family: %w", err)
	}
	return f, nil
}

// FamilyIDForSession returns the family of session row sessionID, so a
// caller's own login is recognised after its token has been rotated.
func (s *Store) FamilyIDForSession(ctx context.Context, sessionID string) (string, error) {
	var familyID string
	err := s.db.QueryRowContext(ctx, `SELECT family_id FROM system.sessions WHERE id = $1`, sessionID).Scan(&familyID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrSessionNotFound
		}
		return "", fmt.Errorf("get session family: %w", err)
	}
	return familyID, nil
}

// RevokeFamily revokes every unrevoked row of familyID and returns their
// ids. Revoking the whole family rather than one row keeps a rotation that
// lands between reading the live row and revoking it from leaving its
// successor live.
func (s *Store) RevokeFamily(ctx context.Context, familyID, reason string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		UPDATE system.sessions SET revoked_at = NOW(), revoke_reason = $2
		WHERE family_id = $1 AND revoked_at IS NULL
		RETURNING id
	`, familyID, reason)
	if err != nil {
		return nil, fmt.Errorf("revoke session family: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan revoked session id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate revoked session ids: %w", err)
	}
	return ids, nil
}
