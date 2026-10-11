package tenantconfig

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// SeedModuleConfig inserts manifest defaults without replacing existing tenant values.
// Manifest keys are qualified; module_config stores the module and short key separately.
func (s *Store) SeedModuleConfig(ctx context.Context, slug string, mf manifest.Manifest) error {
	if len(mf.TenantConfigSeeds) == 0 {
		return nil
	}

	entries := make(map[string]manifest.ConfigEntry, len(mf.ConfigSchema))
	for _, entry := range mf.ConfigSchema {
		entries[entry.Key] = entry
	}

	values := make([]ModuleConfigValue, 0, len(mf.TenantConfigSeeds))
	for _, key := range slices.Sorted(maps.Keys(mf.TenantConfigSeeds)) {
		shortKey, qualified := strings.CutPrefix(key, mf.Name+".")
		entry, declared := entries[shortKey]
		if !qualified || !declared {
			return fmt.Errorf("seed config %q: key must name a config_schema entry owned by %s", key, mf.Name)
		}

		data, err := json.Marshal(mf.TenantConfigSeeds[key], json.Deterministic(true))
		if err != nil {
			return fmt.Errorf("encode config seed %s: %w", key, err)
		}

		values = append(values, ModuleConfigValue{Key: shortKey, Value: data, Type: entry.Type})
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin seed module config: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := fmt.Sprintf(`
		INSERT INTO %s.module_config (module_name, key, value, value_type)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (module_name, key) DO NOTHING
	`, tenantschema.Name(slug))
	for _, value := range values {
		if _, err := tx.ExecContext(ctx, query, mf.Name, value.Key, value.Value, value.Type); err != nil {
			return fmt.Errorf("seed config %s.%s: %w", mf.Name, value.Key, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit seed module config: %w", err)
	}

	return nil
}
