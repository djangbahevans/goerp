package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Execer lets partition registration share a caller's transaction or use a pool directly.
type Execer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

const (
	PartitionInterval = "1 month"
	PartitionPremake  = 3
)

// RegisterPartition registers a plain, unquoted schema.table with pg_partman. Concurrent
// callers must share an advisory-locked transaction because checking part_config before
// create_parent is not atomic.
func RegisterPartition(ctx context.Context, exec Execer, parentTable, controlColumn string) error {
	var alreadyRegistered bool
	checkQuery := "SELECT EXISTS(SELECT 1 FROM partman.part_config WHERE parent_table = $1)"
	if err := exec.QueryRowContext(ctx, checkQuery, parentTable).Scan(&alreadyRegistered); err != nil {
		return fmt.Errorf("check partman registration for %s: %w", parentTable, err)
	}
	if alreadyRegistered {
		return nil
	}

	_, err := exec.ExecContext(ctx,
		"SELECT partman.create_parent(p_parent_table := $1, p_control := $2, p_interval := $3, p_premake := $4)",
		parentTable, controlColumn, PartitionInterval, PartitionPremake)
	if err != nil {
		return fmt.Errorf("register %s with pg_partman: %w", parentTable, err)
	}
	return nil
}
