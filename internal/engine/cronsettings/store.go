// Package cronsettings persists tenant cron choices and serializes execution admission with toggles.
package cronsettings

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5"
)

const TableName = "cron_job_settings"

const createTable = `CREATE TABLE IF NOT EXISTS %s.cron_job_settings (
    module_name TEXT NOT NULL,
    cron_name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL,
    generation UUID NOT NULL DEFAULT uuidv7(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_by UUID,
    PRIMARY KEY (module_name, cron_name)
)`

var (
	ErrUnavailable = errors.New("cron settings unavailable")
	ErrConflict    = errors.New("cron settings generation conflict")
	ErrObsolete    = errors.New("cron run disabled or obsolete")
)

type State struct {
	Enabled    bool
	Generation string
	UpdatedAt  time.Time
	UpdatedBy  sql.NullString
}

type Identity struct {
	Module string
	Name   string
}

type Store struct {
	db *sql.DB
}

func NewStore(pool *sql.DB) *Store {
	return &Store{db: pool}
}

func (s *Store) Bootstrap(ctx context.Context, slug string) error {
	return db.WithAdvisoryLock(ctx, s.db, []int64{db.AdvisoryLockKey("cronsettings:" + slug)}, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, fmt.Sprintf(createTable, tenantschema.Name(slug)))
		return err
	})
}

func (s *Store) Initialize(ctx context.Context, slug, module string, jobs []manifest.CronJob) error {
	if err := s.Bootstrap(ctx, slug); err != nil {
		return fmt.Errorf("create cron settings: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin cron initialization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Cleanup takes the exclusive form of this lock across all tenant schemas.
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared($1)", moduleLock(module)); err != nil {
		return fmt.Errorf("lock cron initialization: %w", err)
	}

	for _, job := range jobs {
		_, err := tx.ExecContext(ctx, "INSERT INTO "+tenantschema.Name(slug)+`.cron_job_settings
			(module_name, cron_name, enabled) VALUES ($1, $2, $3)
			ON CONFLICT (module_name, cron_name) DO NOTHING`, module, job.Name, job.IsEnabledByDefault())
		if err != nil {
			return fmt.Errorf("initialize cron job %s/%s: %w", module, job.Name, err)
		}
	}

	return tx.Commit()
}

// RemoveModule deletes choices before uninstall completion. The caller holds the
// module registry reservation through removal and publication of the uninstall.
func (s *Store) RemoveModule(ctx context.Context, module string) error {
	return db.WithAdvisoryLock(ctx, s.db, []int64{moduleLock(module)}, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT n.nspname FROM pg_namespace n
			JOIN pg_class c ON c.relnamespace = n.oid
			WHERE n.nspname LIKE 'tenant\_%' AND c.relname = 'cron_job_settings' AND c.relkind = 'r'`)
		if err != nil {
			return fmt.Errorf("list cron settings schemas: %w", err)
		}

		var schemas []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				_ = rows.Close()
				return err
			}
			schemas = append(schemas, name)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}

		for _, name := range schemas {
			_, err := tx.ExecContext(ctx, "DELETE FROM "+pgx.Identifier{name, TableName}.Sanitize()+" WHERE module_name = $1", module)
			if err != nil {
				return fmt.Errorf("remove module cron settings: %w", err)
			}
		}
		return nil
	})
}

func moduleLock(module string) int64 {
	return db.AdvisoryLockKey("cronsettings:module:" + module)
}

func (s *Store) Read(ctx context.Context, slug string, identities []Identity) (map[Identity]State, error) {
	states := make(map[Identity]State, len(identities))
	if len(identities) == 0 {
		return states, nil
	}

	modules := make([]string, len(identities))
	names := make([]string, len(identities))
	for i, identity := range identities {
		modules[i], names[i] = identity.Module, identity.Name
	}

	rows, err := s.db.QueryContext(ctx, `SELECT s.module_name, s.cron_name, s.enabled, s.generation, s.updated_at, s.updated_by
		FROM `+tenantschema.Name(slug)+`.cron_job_settings s
		JOIN unnest($1::text[], $2::text[]) AS wanted(module_name, cron_name)
		ON s.module_name = wanted.module_name AND s.cron_name = wanted.cron_name`, modules, names)
	if err != nil {
		return nil, fmt.Errorf("%w: read settings: %w", ErrUnavailable, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var identity Identity
		var state State
		if err := rows.Scan(&identity.Module, &identity.Name, &state.Enabled, &state.Generation, &state.UpdatedAt, &state.UpdatedBy); err != nil {
			return nil, fmt.Errorf("%w: scan settings: %w", ErrUnavailable, err)
		}
		states[identity] = state
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate settings: %w", ErrUnavailable, err)
	}
	if len(states) != len(identities) {
		return nil, ErrUnavailable
	}

	return states, nil
}

func readLocked(ctx context.Context, tx *sql.Tx, slug string, identity Identity, lock string) (State, error) {
	var state State
	err := tx.QueryRowContext(ctx, `SELECT enabled, generation, updated_at, updated_by FROM `+tenantschema.Name(slug)+
		`.cron_job_settings WHERE module_name = $1 AND cron_name = $2 FOR `+lock, identity.Module, identity.Name).
		Scan(&state.Enabled, &state.Generation, &state.UpdatedAt, &state.UpdatedBy)
	if err != nil {
		return State{}, fmt.Errorf("%w: lock settings: %w", ErrUnavailable, err)
	}
	return state, nil
}

func (s *Store) SetEnabled(ctx context.Context, tenantID, slug string, identity Identity, enabled bool, expectedGeneration, actor string) (State, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, fmt.Errorf("%w: begin toggle: %w", ErrUnavailable, err)
	}
	defer func() { _ = tx.Rollback() }()

	state, err := readLocked(ctx, tx, slug, identity, "UPDATE")
	if err != nil {
		return State{}, err
	}
	if state.Enabled != enabled {
		if state.Generation != expectedGeneration {
			return State{}, ErrConflict
		}

		previous := state.Enabled
		err := tx.QueryRowContext(ctx, "UPDATE "+tenantschema.Name(slug)+`.cron_job_settings
			SET enabled = $3, generation = uuidv7(), updated_at = clock_timestamp(), updated_by = $4
			WHERE module_name = $1 AND cron_name = $2 RETURNING enabled, generation, updated_at, updated_by`,
			identity.Module, identity.Name, enabled, actor).Scan(&state.Enabled, &state.Generation, &state.UpdatedAt, &state.UpdatedBy)
		if err != nil {
			return State{}, fmt.Errorf("%w: update settings: %w", ErrUnavailable, err)
		}

		metadata, err := json.Marshal(map[string]any{
			"module":           identity.Module,
			"cron_name":        identity.Name,
			"previous_enabled": previous,
			"enabled":          enabled,
			"generation":       state.Generation,
		})
		if err != nil {
			return State{}, fmt.Errorf("encode cron audit: %w", err)
		}
		event := "cron.disabled"
		if enabled {
			event = "cron.enabled"
		}
		if err := authaudit.NewStore(s.db, nil).InsertTx(ctx, tx, authaudit.Row{
			EventType:   event,
			TenantID:    tenantID,
			ActorUserID: actor,
			Success:     true,
			Metadata:    metadata,
		}); err != nil {
			return State{}, fmt.Errorf("%w: record cron toggle audit: %w", ErrUnavailable, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return State{}, fmt.Errorf("%w: commit cron toggle: %w", ErrUnavailable, err)
	}
	return state, nil
}

// Admit releases the settings lock before returning, so handlers never hold it.
func (s *Store) Admit(ctx context.Context, slug string, identity Identity, generation string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin cron admission: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	state, err := readLocked(ctx, tx, slug, identity, "SHARE")
	if err != nil {
		return err
	}
	if !state.Enabled || state.Generation != generation {
		return ErrObsolete
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit cron admission: %w", err)
	}
	return nil
}
