package jobqueue

import (
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/jackc/pgx/v5"
	"github.com/parquet-go/parquet-go"
	"github.com/riverqueue/river"
)

type PartitionArchiveArgs struct{}

func (PartitionArchiveArgs) Kind() string { return "partition_archive" }

func (PartitionArchiveArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAdmin}
}

type PartitionArchiveWorker struct {
	river.WorkerDefaults[PartitionArchiveArgs]
	Pool    *sql.DB
	Backend storage.Backend
}

func (*PartitionArchiveWorker) Timeout(*river.Job[PartitionArchiveArgs]) time.Duration {
	return 30 * time.Minute
}

type archivePartition struct {
	schema string
	table  string
	name   string
}

func (w *PartitionArchiveWorker) Work(ctx context.Context, _ *river.Job[PartitionArchiveArgs]) error {
	if w.Backend == nil {
		return fmt.Errorf("partition archive requires a storage backend")
	}

	// Bounds come from PostgreSQL, not partition names: a partial month must stay live.
	rows, err := w.Pool.QueryContext(ctx, `
		SELECT n.nspname, p.relname, c.relname
		FROM pg_inherits i
		JOIN pg_class p ON p.oid = i.inhparent
		JOIN pg_namespace n ON n.oid = p.relnamespace
		JOIN pg_class c ON c.oid = i.inhrelid AND c.relnamespace = p.relnamespace
		WHERE starts_with(n.nspname, 'tenant_')
		  AND p.relname IN ('event_log', 'audit_log')
		  AND (regexp_match(pg_get_expr(c.relpartbound, c.oid),
		      '^FOR VALUES FROM \(''[^'']+''\) TO \(''([^'']+)''\)$'))[1]::timestamptz <= $1
		ORDER BY n.nspname, p.relname, c.relname`, time.Now().UTC().AddDate(-1, 0, 0))
	if err != nil {
		return fmt.Errorf("list archive partitions: %w", err)
	}
	defer rows.Close()

	var partitions []archivePartition
	for rows.Next() {
		var p archivePartition
		if err := rows.Scan(&p.schema, &p.table, &p.name); err != nil {
			return fmt.Errorf("scan archive partition: %w", err)
		}
		partitions = append(partitions, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list archive partitions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close archive partition list: %w", err)
	}

	for _, p := range partitions {
		if err := w.archive(ctx, p); err != nil {
			return fmt.Errorf("archive %s: %w", pgx.Identifier{p.schema, p.name}.Sanitize(), err)
		}
	}

	return nil
}

func (w *PartitionArchiveWorker) archive(ctx context.Context, p archivePartition) error {
	tx, err := w.Pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin partition archive: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '5s'`); err != nil {
		return fmt.Errorf("set archive lock timeout: %w", err)
	}

	parent := pgx.Identifier{p.schema, p.table}.Sanitize()
	partition := pgx.Identifier{p.schema, p.name}.Sanitize()
	// Lock the parent first so inserts cannot deadlock with DETACH's lock upgrade.
	// Both locks keep the exported snapshot stable through removal, including direct writes.
	if _, err := tx.ExecContext(ctx, "LOCK TABLE ONLY "+parent+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		return fmt.Errorf("lock archive parent: %w", err)
	}

	var attached bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_inherits
		WHERE inhparent = $1::regclass AND inhrelid = to_regclass($2)
	)`, parent, partition).Scan(&attached); err != nil {
		return fmt.Errorf("check archive partition attachment: %w", err)
	}
	if !attached {
		return nil
	}

	if _, err := tx.ExecContext(ctx, "LOCK TABLE "+partition+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		return fmt.Errorf("lock archive partition: %w", err)
	}

	key := fmt.Sprintf("archive/%s/%s/%s.parquet.gz", strings.TrimPrefix(p.schema, "tenant_"), p.table, p.name)
	if err := w.export(ctx, tx, p, key); err != nil {
		return fmt.Errorf("export partition: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE "+parent+" DETACH PARTITION "+partition); err != nil {
		return fmt.Errorf("detach partition: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE "+partition); err != nil {
		return fmt.Errorf("drop partition: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit partition removal: %w", err)
	}

	return nil
}

func (w *PartitionArchiveWorker) export(ctx context.Context, tx *sql.Tx, p archivePartition, key string) error {
	exists, err := w.Backend.Exists(ctx, key)
	if err != nil {
		return fmt.Errorf("check archive object: %w", err)
	}

	file, err := os.CreateTemp("", "goerp-partition-archive-*")
	if err != nil {
		return fmt.Errorf("create archive spool: %w", err)
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()

	var archived *parquet.GenericReader[any]
	if exists {
		if err := w.downloadArchive(ctx, key, file); err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil {
			return fmt.Errorf("stat archive spool: %w", err)
		}
		pf, err := parquet.OpenFile(file, info.Size())
		if err != nil {
			return fmt.Errorf("open archived parquet: %w", err)
		}
		archived = parquet.NewGenericReader[any](pf)
		defer func() { _ = archived.Close() }()
	}

	partition := pgx.Identifier{p.schema, p.name}.Sanitize()
	timeKey := "emitted_at"
	if p.table == "audit_log" {
		timeKey = "changed_at"
	}
	rows, err := tx.QueryContext(ctx, "SELECT * FROM "+partition+" ORDER BY "+pgx.Identifier{"id"}.Sanitize()+", "+pgx.Identifier{timeKey}.Sanitize())
	if err != nil {
		return fmt.Errorf("read partition rows: %w", err)
	}
	defer rows.Close()

	columns, err := rows.ColumnTypes()
	if err != nil {
		return fmt.Errorf("read partition column types: %w", err)
	}
	schema, err := archiveSchema(columns)
	if err != nil {
		return err
	}

	var writer *parquet.GenericWriter[any]
	var compressed *gzip.Writer
	if archived == nil {
		compressed = gzip.NewWriter(file)
		writer = parquet.NewGenericWriter[any](compressed, schema, parquet.MaxRowsPerRowGroup(4096))
	} else if !parquet.EqualNodes(schema, archived.Schema()) {
		return fmt.Errorf("existing archive schema differs from partition; retain partition")
	}

	values := make([]any, len(columns))
	args := make([]any, len(columns))
	for i := range args {
		args[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(args...); err != nil {
			return fmt.Errorf("scan partition row: %w", err)
		}
		record := make(map[string]any, len(columns))
		for i, column := range columns {
			value := values[i]
			if data, ok := value.([]byte); ok && column.DatabaseTypeName() != "BYTEA" {
				value = string(data)
			}
			record[column.Name()] = value
		}

		row := schema.Deconstruct(nil, record)
		if writer != nil {
			if _, err := writer.WriteRows([]parquet.Row{row}); err != nil {
				return fmt.Errorf("write parquet row: %w", err)
			}
		} else {
			// A failed removal can leave an archive while late events still enter PostgreSQL.
			// Verify every value before reusing that object, without rewriting it.
			var saved [1]parquet.Row
			n, err := archived.ReadRows(saved[:])
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("read archived row: %w", err)
			}
			if n != 1 || !row.Equal(saved[0]) {
				return fmt.Errorf("existing archive differs from partition; retain partition")
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read partition rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close partition rows: %w", err)
	}

	if archived != nil {
		var saved [1]parquet.Row
		n, err := archived.ReadRows(saved[:])
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("finish archived rows: %w", err)
		}
		if n != 0 {
			return fmt.Errorf("existing archive has extra rows; retain partition")
		}
		return nil
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish parquet archive: %w", err)
	}
	if err := compressed.Close(); err != nil {
		return fmt.Errorf("finish gzip archive: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind archive spool: %w", err)
	}
	if _, err := w.Backend.Upload(ctx, key, file, storage.UploadOptions{ContentType: "application/gzip"}); err != nil {
		return fmt.Errorf("upload archive: %w", err)
	}

	return nil
}

func (w *PartitionArchiveWorker) downloadArchive(ctx context.Context, key string, dest io.Writer) error {
	object, _, err := w.Backend.Download(ctx, key)
	if err != nil {
		return fmt.Errorf("download existing archive: %w", err)
	}
	defer func() { _ = object.Close() }()

	compressed, err := gzip.NewReader(object)
	if err != nil {
		return fmt.Errorf("open gzip archive: %w", err)
	}
	defer func() { _ = compressed.Close() }()

	if _, err := io.Copy(dest, compressed); err != nil {
		return fmt.Errorf("decompress existing archive: %w", err)
	}
	return nil
}

func archiveSchema(columns []*sql.ColumnType) (*parquet.Schema, error) {
	group := make(parquet.Group, len(columns))
	for _, column := range columns {
		var node parquet.Node
		switch column.DatabaseTypeName() {
		case "TEXT", "UUID":
			node = parquet.String()
		case "BYTEA":
			node = parquet.Leaf(parquet.ByteArrayType)
		case "JSONB":
			node = parquet.JSON()
		case "INT2", "INT4", "INT8":
			node = parquet.Leaf(parquet.Int64Type)
		case "TIMESTAMPTZ":
			node = parquet.Timestamp(parquet.Microsecond)
		default:
			return nil, fmt.Errorf("unsupported archive column %s type %s", column.Name(), column.DatabaseTypeName())
		}
		group[column.Name()] = parquet.Optional(node)
	}

	return parquet.NewSchema("archive", group), nil
}
