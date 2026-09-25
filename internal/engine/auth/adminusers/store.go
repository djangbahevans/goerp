package adminusers

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

var (
	errUserNotFound = errors.New("user not found")
	errStateChanged = errors.New("user status does not allow this transition")
)

// entry is one row of a tenant's user directory: a member (a live
// user_roles grant) or an invitee (a pending tenant_invitations row and no
// membership yet). Status is "invited" for an invitee and users.status for
// a member.
type entry struct {
	ID           string
	Email        string
	Status       string
	LastLoginAt  *time.Time
	InvitationID *string
	Name         *string
	AvatarFileID *string
	Phone        *string
	Roles        []string
}

type invitation struct {
	ID        string
	Role      string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type listFilter struct {
	Search string
	Status string
	Role   string
	After  string
	Limit  int
}

// AuditRecorder is satisfied by authaudit.Store.
type AuditRecorder interface {
	Insert(ctx context.Context, row authaudit.Row) error
	InsertTx(ctx context.Context, tx *sql.Tx, row authaudit.Row) error
}

type Store struct {
	db    *sql.DB
	audit AuditRecorder
}

func NewStore(db *sql.DB, audit AuditRecorder) *Store {
	return &Store{db: db, audit: audit}
}

// directoryCTE defines `entries` for a tenant schema. A user is a member
// while any grant is unexpired, the same rule role.Store.IsMember applies.
func directoryCTE(schema string) string {
	return fmt.Sprintf(`
	WITH members AS (
		SELECT DISTINCT user_id FROM %[1]s.user_roles
		WHERE expires_at IS NULL OR expires_at > NOW()
	), entries AS (
		SELECT u.id, u.email, u.status, u.last_login_at, NULL::uuid AS invitation_id
		FROM system.users u JOIN members m ON m.user_id = u.id
		WHERE u.deleted_at IS NULL
		UNION ALL
		SELECT u.id, u.email, 'invited', u.last_login_at, ti.id
		FROM %[1]s.tenant_invitations ti
		JOIN system.users u ON u.email = ti.email AND u.deleted_at IS NULL
		WHERE ti.accepted_at IS NULL AND ti.revoked_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM members m WHERE m.user_id = u.id)
	)`, schema)
}

func entryColumns(schema string) string {
	return fmt.Sprintf(`
		e.id, e.email, e.status, e.last_login_at, e.invitation_id, p.name, p.avatar_file_id, p.phone,
		COALESCE((
			SELECT json_agg(r.name ORDER BY r.name)
			FROM %[1]s.user_roles ur JOIN %[1]s.roles r ON r.id = ur.role_id
			WHERE ur.user_id = e.id AND (ur.expires_at IS NULL OR ur.expires_at > NOW())
		), '[]')`, schema)
}

func scanEntry(sc interface{ Scan(dest ...any) error }) (entry, error) {
	var e entry
	var roles []byte
	if err := sc.Scan(&e.ID, &e.Email, &e.Status, &e.LastLoginAt, &e.InvitationID, &e.Name, &e.AvatarFileID, &e.Phone, &roles); err != nil {
		return entry{}, err
	}
	if err := json.Unmarshal(roles, &e.Roles); err != nil {
		return entry{}, fmt.Errorf("decode role names: %w", err)
	}
	return e, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// list returns one page of the directory ordered by email, which is unique
// among non-deleted users and so serves as the keyset, plus the number of
// entries matching the filters across all pages. Role keeps the members
// holding a live grant of that role name.
func (s *Store) list(ctx context.Context, tenantSlug string, f listFilter) ([]entry, int, error) {
	schema := tenantschema.Name(tenantSlug)
	where := fmt.Sprintf(`
		WHERE ($1 = '' OR e.email ILIKE $1 OR p.name ILIKE $1)
		  AND ($2 = '' OR e.status = $2)
		  AND ($3 = '' OR EXISTS (
			SELECT 1 FROM %[1]s.user_roles ur JOIN %[1]s.roles r ON r.id = ur.role_id
			WHERE ur.user_id = e.id AND r.name = $3 AND (ur.expires_at IS NULL OR ur.expires_at > NOW())))`, schema)
	search := ""
	if f.Search != "" {
		search = "%" + escapeLike(f.Search) + "%"
	}

	var total int
	countQuery := directoryCTE(schema) + ` SELECT COUNT(*) FROM entries e LEFT JOIN system.user_profiles p ON p.user_id = e.id` + where
	if err := s.db.QueryRowContext(ctx, countQuery, search, f.Status, f.Role).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count directory entries: %w", err)
	}

	pageQuery := directoryCTE(schema) + ` SELECT ` + entryColumns(schema) + `
		FROM entries e LEFT JOIN system.user_profiles p ON p.user_id = e.id` + where + `
		  AND ($4 = '' OR e.email > $4)
		ORDER BY e.email
		LIMIT $5`
	rows, err := s.db.QueryContext(ctx, pageQuery, search, f.Status, f.Role, f.After, f.Limit)
	if err != nil {
		return nil, 0, fmt.Errorf("query directory entries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	entries := []entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan directory entry: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate directory entries: %w", err)
	}
	return entries, total, nil
}

// get returns userID's directory entry, or errUserNotFound when the user
// is neither a member of nor invited to the tenant.
func (s *Store) get(ctx context.Context, tenantSlug, userID string) (entry, error) {
	schema := tenantschema.Name(tenantSlug)
	query := directoryCTE(schema) + ` SELECT ` + entryColumns(schema) + `
		FROM entries e LEFT JOIN system.user_profiles p ON p.user_id = e.id
		WHERE e.id = $1`
	e, err := scanEntry(s.db.QueryRowContext(ctx, query, userID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entry{}, errUserNotFound
		}
		return entry{}, fmt.Errorf("get directory entry: %w", err)
	}
	return e, nil
}

// liveInvitation returns email's pending invitation in the tenant, or nil.
func (s *Store) liveInvitation(ctx context.Context, tenantSlug, email string) (*invitation, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		SELECT ti.id, r.name, ti.expires_at, ti.created_at
		FROM %[1]s.tenant_invitations ti JOIN %[1]s.roles r ON r.id = ti.role_id
		WHERE ti.email = $1 AND ti.accepted_at IS NULL AND ti.revoked_at IS NULL`, schema)
	var inv invitation
	err := s.db.QueryRowContext(ctx, query, email).Scan(&inv.ID, &inv.Role, &inv.ExpiresAt, &inv.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get live invitation: %w", err)
	}
	return &inv, nil
}

// transition runs one users.status change and its audit row in a single
// transaction (auth-internals.md §2 "Transactional audit"). The UPDATE is
// guarded by condition; errStateChanged means it matched no row.
func (s *Store) transition(ctx context.Context, userID, set, condition string, audit authaudit.Row) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin status transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `UPDATE system.users SET `+set+`, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL AND `+condition, userID)
	if err != nil {
		return fmt.Errorf("update user status: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update user status: %w", err)
	}
	if n == 0 {
		return errStateChanged
	}
	if err := s.audit.InsertTx(ctx, tx, audit); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit status transition: %w", err)
	}
	return nil
}

// suspend only applies to an active user, so unsuspend's return to active
// can't skip invite acceptance or email verification.
func (s *Store) suspend(ctx context.Context, userID string, audit authaudit.Row) error {
	return s.transition(ctx, userID, "status = 'suspended'", "status = 'active'", audit)
}

func (s *Store) unsuspend(ctx context.Context, userID string, audit authaudit.Row) error {
	return s.transition(ctx, userID, "status = 'active'", "status = 'suspended'", audit)
}

func (s *Store) softDelete(ctx context.Context, userID string, audit authaudit.Row) error {
	return s.transition(ctx, userID, "status = 'deleted', deleted_at = NOW()", "TRUE", audit)
}
