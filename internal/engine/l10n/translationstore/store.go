// Package translationstore persists backend module translations and provides cached exact lookups.
package translationstore

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

const createTable = `
CREATE TABLE IF NOT EXISTS system.module_translations (
 module_name TEXT NOT NULL,
 locale TEXT NOT NULL,
 translation_key TEXT NOT NULL,
 translation TEXT NOT NULL,
 PRIMARY KEY (module_name, locale, translation_key)
)`

type lookupKey struct {
	locale string
	key    string
}

type Store struct {
	db      *sql.DB
	mu      sync.Mutex
	current atomic.Pointer[map[lookupKey]string]
}

func New(pool *sql.DB) *Store {
	return &Store{db: pool}
}

func (s *Store) Bootstrap(ctx context.Context) error {
	return db.WithAdvisoryLock(ctx, s.db, []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("translationstore.Bootstrap")}, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createTable); err != nil {
			return fmt.Errorf("create module translations table: %w", err)
		}
		return nil
	})
}

// Apply merges supplied keys, deletes removed modules, and refreshes the cache from
// committed storage. Omitted keys and locales survive a merge. Nil disables persistence.
func (s *Store) Apply(ctx context.Context, updates map[string]map[string]map[string]string, removed []string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var next map[lookupKey]string
	err := db.WithAdvisoryLock(ctx, s.db, []int64{db.AdvisoryLockKey("translationstore.Apply")}, func(tx *sql.Tx) error {
		for _, name := range slices.Sorted(maps.Keys(updates)) {
			locales := updates[name]
			for _, locale := range slices.Sorted(maps.Keys(locales)) {
				entries := locales[locale]
				for _, key := range slices.Sorted(maps.Keys(entries)) {
					if _, err := tx.ExecContext(ctx, `INSERT INTO system.module_translations
      (module_name, locale, translation_key, translation) VALUES ($1, $2, $3, $4)
      ON CONFLICT (module_name, locale, translation_key) DO UPDATE SET translation = EXCLUDED.translation`,
						name, locale, name+"."+key, entries[key]); err != nil {
						return fmt.Errorf("store %s/%s translation %q: %w", name, locale, key, err)
					}
				}
			}
		}
		for _, name := range removed {
			if _, err := tx.ExecContext(ctx, `DELETE FROM system.module_translations WHERE module_name = $1`, name); err != nil {
				return fmt.Errorf("remove %s translations: %w", name, err)
			}
		}

		rows, err := tx.QueryContext(ctx, `SELECT locale, translation_key, translation FROM system.module_translations`)
		if err != nil {
			return fmt.Errorf("read translations: %w", err)
		}
		defer func() { _ = rows.Close() }()

		next = make(map[lookupKey]string)
		for rows.Next() {
			var key lookupKey
			var translation string
			if err := rows.Scan(&key.locale, &key.key, &translation); err != nil {
				return fmt.Errorf("scan translation: %w", err)
			}
			next[key] = translation
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read translation rows: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.current.Store(&next)
	return nil
}

// Lookup performs an exact locale/key lookup without database access. key includes the
// module namespace; the boolean distinguishes missing keys from empty translations.
func (s *Store) Lookup(locale, key string) (string, bool) {
	if s == nil {
		return "", false
	}
	current := s.current.Load()
	if current == nil {
		return "", false
	}
	value, ok := (*current)[lookupKey{locale: locale, key: key}]
	return value, ok
}
