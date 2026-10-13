package tenantimport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/checkpoint"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenant/transfer"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/riverqueue/river"
)

type Inviter interface {
	InviteRestoredMember(ctx context.Context, tenantSlug, email, roleName, name string) (*invite.Invitation, error)
}

func (w *Worker) checkpointStep(ctx context.Context, job *river.Job[Args], t *tenant.Tenant, name string, action func() error) error {
	staleAfter := w.LeaseStaleAfter
	if staleAfter <= 0 {
		staleAfter = defaultLeaseStaleAfter
	}
	progress, err := w.Checkpoints.AcquireLease(ctx, strconv.FormatInt(job.ID, 10), name, t.ID, fmt.Sprintf("%d-%d", job.ID, job.Attempt), staleAfter)
	if errors.Is(err, checkpoint.ErrAlreadyComplete) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := action(); err != nil {
		_ = progress.MarkFailed(ctx)
		return err
	}
	return progress.MarkComplete(ctx)
}

func (w *Worker) loadEngineTable(ctx context.Context, t *tenant.Tenant, table transfer.Table, data []byte, mapping accountMapping) error {
	if !transfer.KnownTable(table.Name) {
		return fmt.Errorf("unknown archive engine table %q", table.Name)
	}
	tx, err := beginTenantScopedWrite(ctx, w.RawDB, t.Slug)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	cols, err := transfer.Columns(ctx, tx, "tenant_"+t.Slug, table.Name)
	if err != nil {
		return err
	}

	// Fresh schemas contain seeded defaults with different IDs. Replace seed rows in the
	// same transaction as the first table load, before dependent tables are restored.
	if table.Name == "activity_types" || table.Name == "notification_templates" {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+quoteIdent(table.Name)); err != nil {
			return err
		}
	}
	names := transfer.ColumnNames(cols)
	for i, name := range names {
		names[i] = quoteIdent(name)
	}
	sqlStr := "INSERT INTO " + quoteIdent(table.Name) + " (" + strings.Join(names, ",") + ") SELECT " + strings.Join(names, ",") + " FROM jsonb_populate_record(NULL::" + quoteIdent(table.Name) + ", $1::jsonb) ON CONFLICT DO NOTHING"
	stmt, err := tx.PrepareContext(ctx, sqlStr)
	if err != nil {
		return err
	}
	defer stmt.Close()

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var rec exportRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec, json.WithUnmarshalers(numberPreservingUnmarshalers)); err != nil {
			return err
		}
		if rec.Model != table.Name {
			return fmt.Errorf("engine data for %s names %s", table.Name, rec.Model)
		}
		if table.Name == "user_device_tokens" {
			return fmt.Errorf("archive contains device credentials")
		}
		if table.Name == "module_config" && rec.Record["encrypted"] != false {
			return fmt.Errorf("archive contains encrypted config")
		}
		if err := rewriteIdentities(rec.Record, cols, mapping.IDs, t.ID); err != nil {
			return err
		}
		if table.Name == "files" {
			key, err := importedFileKey(t.ID, rec.Record)
			if err != nil {
				return err
			}
			rec.Record["storage_key"] = key
		}
		encoded, err := json.Marshal(rec.Record)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, string(encoded)); err != nil {
			return fmt.Errorf("insert %s: %w", table.Name, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func importedFileKey(tenantID string, record map[string]any) (string, error) {
	id, _ := record["id"].(string)
	purpose, _ := record["purpose"].(string)
	oldKey, _ := record["storage_key"].(string)
	if _, err := uuid.Parse(id); err != nil {
		return "", err
	}
	if !storage.ValidPurpose(purpose) {
		return "", fmt.Errorf("invalid file purpose %q", purpose)
	}
	parts := strings.Split(oldKey, "/")
	if len(parts) != 5 || parts[0] != purpose || strings.TrimSuffix(parts[4], path.Ext(parts[4])) != id {
		return "", fmt.Errorf("invalid source file key %q", oldKey)
	}
	if _, err := time.Parse("2006/01", parts[2]+"/"+parts[3]); err != nil {
		return "", fmt.Errorf("invalid file key date: %w", err)
	}
	if _, err := uuid.Parse(parts[1]); err != nil {
		return "", err
	}
	parts[1] = tenantID
	return strings.Join(parts, "/"), nil
}

func (w *Worker) restoreFiles(ctx context.Context, job *river.Job[Args], t *tenant.Tenant, data map[string][]byte) error {
	rows, err := w.RawDB.QueryContext(ctx, "SELECT id, storage_key, checksum_sha256, content_type, is_public FROM "+tenantschema.Name(t.Slug)+".files WHERE deleted_at IS NULL ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, key, checksum, contentType string
		var public bool
		if err := rows.Scan(&id, &key, &checksum, &contentType, &public); err != nil {
			return err
		}
		content, ok := data["files/"+id]
		if !ok {
			return fmt.Errorf("archive has no object for file %s", id)
		}
		sum := sha256.Sum256(content)
		if hex.EncodeToString(sum[:]) != checksum {
			return fmt.Errorf("checksum mismatch for file %s", id)
		}
		if err := w.checkpointStep(ctx, job, t, "file:"+id, func() error {
			_, err := w.StorageBackend.Upload(ctx, key, bytes.NewReader(content), storage.UploadOptions{ContentType: contentType, Public: public})
			return err
		}); err != nil {
			return fmt.Errorf("restore file %s: %w", id, err)
		}
	}
	return rows.Err()
}

func (w *Worker) inviteRestoredMembers(ctx context.Context, job *river.Job[Args], t *tenant.Tenant) error {
	schema := tenantschema.Name(t.Slug)
	rows, err := w.RawDB.QueryContext(ctx, fmt.Sprintf(`SELECT DISTINCT ON (u.id) u.id, u.email, COALESCE(p.name, ''), r.name
 FROM %s.tenant_members m JOIN system.users u ON u.id = m.user_id
 JOIN %s.user_roles ur ON ur.user_id = m.user_id JOIN %s.roles r ON r.id = ur.role_id
 LEFT JOIN system.user_profiles p ON p.user_id = u.id
 WHERE u.password_hash IS NULL AND u.deleted_at IS NULL ORDER BY u.id, r.name`, schema, schema, schema))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, email, name, roleName string
		if err := rows.Scan(&id, &email, &name, &roleName); err != nil {
			return err
		}
		if w.Invites == nil {
			return fmt.Errorf("import invitation sender is not configured")
		}
		if err := w.checkpointStep(ctx, job, t, "invite:"+id, func() error {
			_, err := w.Invites.InviteRestoredMember(ctx, t.Slug, email, roleName, name)
			return err
		}); err != nil {
			return fmt.Errorf("invite imported member: %w", err)
		}
	}
	return rows.Err()
}

func (w *Worker) abandonImport(ctx context.Context, t *tenant.Tenant) error {
	current, err := w.TenantStore.GetBySlug(ctx, t.Slug)
	if err != nil {
		return err
	}
	if current.ID != t.ID || current.Status != tenant.StatusProvisioning {
		return nil
	}

	var hasFiles bool
	if err := w.RawDB.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", tenantschema.Name(t.Slug)+".files").Scan(&hasFiles); err != nil {
		return err
	}
	if hasFiles {
		keys, err := files.NewStore(w.RawDB).StorageKeysForTenant(ctx, t.Slug)
		if err != nil {
			return err
		}
		for _, key := range keys {
			exists, err := w.StorageBackend.Exists(ctx, key)
			if err != nil {
				return err
			}
			if exists {
				if err := w.StorageBackend.Delete(ctx, key); err != nil {
					return err
				}
			}
		}
	}
	if err := tenantschema.Drop(ctx, w.RawDB, t.Slug); err != nil {
		return err
	}
	return w.TenantStore.DeleteProvisioning(ctx, t.ID)
}
