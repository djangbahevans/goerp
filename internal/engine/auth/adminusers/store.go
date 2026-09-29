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
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

var errUserNotFound = errors.New("user not found")

// entry is one row of a tenant's user directory: a member (a
// tenant_members row, active or suspended) or an invitee (a pending
// tenant_invitations row and no member row yet). Status is "invited" for
// an invitee and tenant_members.status for a member; AccountSuspended
// reports a platform operator's suspension of the whole account. The
// last sign-in, phone and job title are this tenant's own.
type entry struct {
	ID               string
	Email            string
	Status           string
	AccountSuspended bool
	LastLoginAt      *time.Time
	InvitationID     *string
	Name             *string
	AvatarFileID     *string
	Phone            *string
	JobTitle         *string
	Roles            []string
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

// directoryCTE defines `entries` for a tenant schema.
func directoryCTE(schema string) string {
	return fmt.Sprintf(`
	WITH entries AS (
		SELECT u.id, u.email, tm.status, u.status = 'suspended' AS account_suspended,
		       tm.last_login_at, NULL::uuid AS invitation_id, tm.phone, tm.job_title
		FROM %[1]s.tenant_members tm JOIN system.users u ON u.id = tm.user_id
		WHERE u.deleted_at IS NULL
		UNION ALL
		SELECT u.id, u.email, 'invited', u.status = 'suspended', NULL, ti.id, NULL, NULL
		FROM %[1]s.tenant_invitations ti
		JOIN system.users u ON u.email = ti.email AND u.deleted_at IS NULL
		WHERE ti.accepted_at IS NULL AND ti.revoked_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM %[1]s.tenant_members tm WHERE tm.user_id = u.id)
	)`, schema)
}

func entryColumns(schema string) string {
	return fmt.Sprintf(`
		e.id, e.email, e.status, e.account_suspended, e.last_login_at, e.invitation_id,
		p.name, p.avatar_file_id, e.phone, e.job_title,
		COALESCE((
			SELECT json_agg(r.name ORDER BY r.name)
			FROM %[1]s.user_roles ur JOIN %[1]s.roles r ON r.id = ur.role_id
			WHERE ur.user_id = e.id AND (ur.expires_at IS NULL OR ur.expires_at > NOW())
		), '[]')`, schema)
}

func scanEntry(sc interface{ Scan(dest ...any) error }) (entry, error) {
	var e entry
	var roles []byte
	if err := sc.Scan(&e.ID, &e.Email, &e.Status, &e.AccountSuspended, &e.LastLoginAt, &e.InvitationID, &e.Name, &e.AvatarFileID, &e.Phone, &e.JobTitle, &roles); err != nil {
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

// transition runs one member lifecycle change and its audit row in a
// single transaction (auth-internals.md §2 "Transactional audit"). When
// guardAdmin is set, removing the tenant's last active admin is refused
// with role.ErrLastAdmin first.
func (s *Store) transition(ctx context.Context, tenantSlug, userID string, guardAdmin bool, change func(*sql.Tx) error, audit authaudit.Row) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin member transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if guardAdmin {
		if err := role.GuardLastAdminTx(ctx, tx, tenantSlug, userID); err != nil {
			return err
		}
	}
	if err := change(tx); err != nil {
		return err
	}
	if err := s.audit.InsertTx(ctx, tx, audit); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit member transition: %w", err)
	}
	return nil
}

// suspend suspends an active member in this tenant only; the account and
// its other memberships are untouched.
func (s *Store) suspend(ctx context.Context, tenantSlug, userID, suspendedBy, reason string, audit authaudit.Row) error {
	return s.transition(ctx, tenantSlug, userID, true, func(tx *sql.Tx) error {
		return role.SuspendMemberTx(ctx, tx, tenantSlug, userID, suspendedBy, reason)
	}, audit)
}

func (s *Store) unsuspend(ctx context.Context, tenantSlug, userID string, audit authaudit.Row) error {
	return s.transition(ctx, tenantSlug, userID, false, func(tx *sql.Tx) error {
		return role.UnsuspendMemberTx(ctx, tx, tenantSlug, userID)
	}, audit)
}

// remove deletes the member's role grants and member row in this tenant.
func (s *Store) remove(ctx context.Context, tenantSlug, userID string, audit authaudit.Row) error {
	return s.transition(ctx, tenantSlug, userID, true, func(tx *sql.Tx) error {
		return role.RemoveMemberTx(ctx, tx, tenantSlug, userID)
	}, audit)
}
