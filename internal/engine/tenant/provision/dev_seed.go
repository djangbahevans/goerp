package tenantprovision

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/adminapi"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5"
)

func (d *DevBootstrap) SeedDev(ctx context.Context, moduleName string, batches []adminapi.DevSeedBatch) error {
	t, err := d.activities.tenantStore.GetBySlug(ctx, devSlug)
	if err != nil {
		return err
	}
	if t.Name != devName || t.Status != tenant.StatusActive {
		return fmt.Errorf("an active module-development tenant is required")
	}

	snap := d.activities.registry.Snapshot()
	mod, ok := snap.Modules()[moduleName]
	if !ok {
		return fmt.Errorf("module %q is not loaded", moduleName)
	}

	models := make(map[string]model.ModelDeclaration)
	for _, md := range mod.ModelDecls {
		name := md.QualifiedName(moduleName)
		if md.Backend == "" && slices.Contains(mod.Manifest.Schema.OwnedModels, name) {
			models[name] = md
		}
	}

	tx, err := d.activities.schemaSyncPool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin seed transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, "SET LOCAL statement_timeout = '30s'"); err != nil {
		return err
	}

	for _, batch := range batches {
		md, ok := models[batch.Model]
		if !ok {
			return fmt.Errorf("seed model %q is not a table owned by %s", batch.Model, moduleName)
		}

		allowed := make(map[string]bool)
		for _, field := range md.Fields {
			allowed[field.Name] = true
		}

		table := pgx.Identifier{"tenant_" + t.Slug, modeltable.Name(md)}.Sanitize()
		for i, record := range batch.Records {
			columns := slices.Sorted(maps.Keys(record))
			quoted := make([]string, 0, len(columns))
			for _, column := range columns {
				if column == "tenant_id" || !allowed[column] {
					return fmt.Errorf("seed %s record %d: unknown or protected field %q", batch.Model, i+1, column)
				}
				quoted = append(quoted, pgx.Identifier{column}.Sanitize())
			}

			data, err := json.Marshal(record)
			if err != nil {
				return err
			}

			fields := strings.Join(quoted, ", ")
			if fields != "" {
				fields += ", "
			}
			query := "INSERT INTO " + table + " (" + fields + "tenant_id) SELECT " + fields + "$2::uuid FROM jsonb_populate_record(NULL::" + table + ", $1::jsonb)"
			if _, err := tx.ExecContext(ctx, query, string(data), t.ID); err != nil {
				return fmt.Errorf("seed %s record %d: %w", batch.Model, i+1, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit seed transaction: %w", err)
	}

	return nil
}
