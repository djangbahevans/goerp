package event

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/vmihailenco/msgpack/v5"
)

type SystemJobs interface {
	InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// SystemEmitter records engine events in the durable asynchronous delivery queue.
// A nil emitter disables emission for stores used without a job queue.
type SystemEmitter struct {
	db     *sql.DB
	client SystemJobs
}

func NewSystemEmitter(db *sql.DB, client SystemJobs) *SystemEmitter {
	return &SystemEmitter{db: db, client: client}
}

func (e *SystemEmitter) Emit(ctx context.Context, tenantID, name string, payload map[string]any) error {
	if e == nil {
		return nil
	}

	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin system event: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := e.EmitTx(ctx, tx, tenantID, name, payload); err != nil {
		return err
	}
	return tx.Commit()
}

// EmitTx records the event in the triggering transaction. System handlers run without
// an acting user because account removal and suspension can invalidate that identity.
func (e *SystemEmitter) EmitTx(ctx context.Context, tx *sql.Tx, tenantID, name string, payload map[string]any) error {
	if e == nil {
		return nil
	}
	if tx == nil || tenantID == "" || !strings.HasPrefix(name, "system.") {
		return fmt.Errorf("system event requires a transaction, tenant and system namespace")
	}
	encoded, err := msgpack.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode system event %s: %w", name, err)
	}
	if err := ValidatePayload(encoded); err != nil {
		return err
	}
	args := EventDeliveryArgs{
		EventID:       uuid.NewV7().String(),
		EventName:     name,
		EmitterModule: "system",
		TenantID:      tenantID,
		TraceID:       httperr.TraceIDFromContext(ctx),
		Payload:       encoded,
		EmittedAt:     time.Now(),
		Transactional: true,
	}
	_, err = e.client.InsertTx(ctx, tx, args, nil)
	if err != nil {
		return fmt.Errorf("enqueue system event %s: %w", name, err)
	}
	return nil
}

// UserCreatedTx captures the profile visible when an account joins a tenant.
func (e *SystemEmitter) UserCreatedTx(ctx context.Context, tx *sql.Tx, tenantSlug, userID string) error {
	if e == nil {
		return nil
	}

	var tenantID, email string
	var name, locale *string
	err := tx.QueryRowContext(ctx, `
 SELECT t.id, u.email, p.name, p.locale
 FROM system.tenants t CROSS JOIN system.users u
 LEFT JOIN system.user_profiles p ON p.user_id = u.id
 WHERE t.slug = $1 AND u.id = $2`, tenantSlug, userID).Scan(&tenantID, &email, &name, &locale)
	if err != nil {
		return fmt.Errorf("read created user event fields: %w", err)
	}
	return e.EmitTx(ctx, tx, tenantID, "system.user.created", map[string]any{
		"user_id":   userID,
		"tenant_id": tenantID,
		"email":     email,
		"name":      name,
		"locale":    locale,
	})
}

// ForUserTx fans a shared-account change out to its tenant memberships.
func (e *SystemEmitter) ForUserTx(ctx context.Context, tx *sql.Tx, userID, name string, payload map[string]any) error {
	if e == nil {
		return nil
	}

	rows, err := tx.QueryContext(ctx, `SELECT tenant_id FROM system.tenant_memberships WHERE user_id = $1 ORDER BY tenant_id`, userID)
	if err != nil {
		return fmt.Errorf("read event tenant memberships: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if name == "system.user.password_reset" {
			payload["tenant_id"] = id
		}
		if err := e.EmitTx(ctx, tx, id, name, payload); err != nil {
			return err
		}
	}
	return nil
}

func (e *SystemEmitter) ModuleTenantIDs(ctx context.Context, moduleName string) ([]string, error) {
	if e == nil {
		return nil, nil
	}
	rows, err := e.db.QueryContext(ctx, `
 SELECT v.tenant_id FROM system.module_schema_versions v
 JOIN system.tenants t ON t.id = v.tenant_id
 WHERE v.module_name = $1 AND t.status != 'deleted'
 ORDER BY v.tenant_id`, moduleName)
	if err != nil {
		return nil, fmt.Errorf("read installed module tenants: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan installed module tenant: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read installed module tenants: %w", err)
	}
	return ids, nil
}
