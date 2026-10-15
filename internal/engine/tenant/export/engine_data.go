package tenantexport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/checkpoint"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenant/transfer"
	"github.com/riverqueue/river"
)

func (w *Worker) exportEngineData(ctx context.Context, job *river.Job[Args], t *tenant.Tenant, man *manifest, data map[string][]byte, snap *registry.RegistrySnapshot, prefix string, staleAfter time.Duration) error {
	man.Tables = transfer.Tables()
	for _, table := range man.Tables {
		key := prefix + "/" + table.File
		progress, err := w.Checkpoints.AcquireLease(ctx, strconv.FormatInt(job.ID, 10), "engine:"+table.Name, t.ID, fmt.Sprintf("%d-%d", job.ID, job.Attempt), staleAfter)
		if err != nil && !errors.Is(err, checkpoint.ErrAlreadyComplete) {
			return err
		}
		if err == nil {
			content, dumpErr := w.dumpEngineTable(ctx, t.Slug, table.Name)
			if dumpErr == nil {
				_, dumpErr = w.StorageBackend.Upload(ctx, key, bytes.NewReader(content), storage.UploadOptions{ContentType: "application/x-ndjson"})
			}
			if dumpErr != nil {
				_ = progress.MarkFailed(ctx)
				return fmt.Errorf("export engine table %s: %w", table.Name, dumpErr)
			}
			if err := progress.MarkComplete(ctx); err != nil {
				return err
			}
		}

		content, err := w.download(ctx, key)
		if err != nil {
			return err
		}
		data[table.File] = content
	}

	tx, err := beginTenantScopedRead(ctx, w.RawDB, t.Slug)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	refs := make(map[string]bool)

	collect := func(content []byte, tables map[string]string) error {
		columns := make(map[string][]transfer.Column)
		scanner := bufio.NewScanner(bytes.NewReader(content))
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			var rec transfer.Record
			if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
				return err
			}
			if rec.Relation != nil {
				continue
			}
			cols, ok := columns[rec.Model]
			if !ok {
				table, ok := tables[rec.Model]
				if !ok {
					return fmt.Errorf("unknown archived model %q", rec.Model)
				}
				cols, err = transfer.Columns(ctx, tx, "tenant_"+t.Slug, table)
				if err != nil {
					return err
				}
				columns[rec.Model] = cols
			}
			for _, col := range cols {
				if !col.User {
					continue
				}
				value := rec.Record[col.Name]
				if len(value) == 0 || string(value) == "null" {
					continue
				}
				var id string
				if err := json.Unmarshal(value, &id); err != nil {
					return err
				}
				refs[id] = true
			}
		}
		return scanner.Err()
	}
	for _, table := range man.Tables {
		if err := collect(data[table.File], map[string]string{table.Name: table.Name}); err != nil {
			return err
		}
	}
	for _, mod := range man.Modules {
		tables := make(map[string]string)
		for _, md := range snap.Models(mod.Name) {
			tables[md.Name] = modeltable.Name(md)
		}
		if err := collect(data[mod.Name], tables); err != nil {
			return err
		}
	}

	ids := slices.Sorted(maps.Keys(refs))

	accounts := make([]transfer.Account, 0, len(ids))
	for _, id := range ids {
		var account transfer.Account
		if err := tx.QueryRowContext(ctx, `SELECT u.id, u.email, COALESCE(p.name, '') FROM system.users u LEFT JOIN system.user_profiles p ON p.user_id = u.id WHERE u.id = $1`, id).Scan(&account.ID, &account.Email, &account.Name); err != nil {
			return fmt.Errorf("resolve referenced account %s: %w", id, err)
		}
		accounts = append(accounts, account)
	}
	data["accounts.json"], err = json.Marshal(accounts)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(bytes.NewReader(data["engine/files.jsonl"]))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var rec transfer.Record
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			return err
		}
		if string(rec.Record["deleted_at"]) != "null" {
			continue
		}
		var id, key string
		if err := json.Unmarshal(rec.Record["id"], &id); err != nil {
			return err
		}
		if err := json.Unmarshal(rec.Record["storage_key"], &key); err != nil {
			return err
		}
		content, err := w.download(ctx, key)
		if err != nil {
			return fmt.Errorf("export file %s: %w", id, err)
		}
		file := "files/" + id
		data[file] = content
		man.Objects = append(man.Objects, file)
	}
	return scanner.Err()
}

func (w *Worker) download(ctx context.Context, key string) ([]byte, error) {
	rc, _, err := w.StorageBackend.Download(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func (w *Worker) dumpEngineTable(ctx context.Context, slug, table string) ([]byte, error) {
	tx, err := beginTenantScopedRead(ctx, w.RawDB, slug)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	cols, err := transfer.Columns(ctx, tx, "tenant_"+slug, table)
	if err != nil {
		return nil, err
	}
	return transfer.Dump(ctx, tx, table, table, transfer.ColumnNames(cols), transfer.Filter(table), "")
}
