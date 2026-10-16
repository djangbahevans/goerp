package eventdelivery

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/rivertype"
	"github.com/vmihailenco/msgpack/v5"
)

type DLQ struct {
	pool  *pgxpool.Pool
	table string
}

func NewDLQ(pool *pgxpool.Pool, schema string) *DLQ {
	return &DLQ{pool: pool, table: pgx.Identifier{schema, "river_job"}.Sanitize()}
}

type DLQFilter struct {
	TenantID         string `json:"tenant_id"`
	EventName        string `json:"event_name"`
	SubscriberModule string `json:"subscriber_module"`
}

const dlqWhere = `kind = 'subscriber_delivery' AND state = 'discarded'
	AND ($1 = '' OR args->>'tenant_id' = $1)
	AND ($2 = '' OR args->>'event_name' = $2)
	AND ($3 = '' OR args->>'module_name' = $3)`

type DLQItem struct {
	ID               string     `json:"id"`
	EventID          string     `json:"event_id"`
	EventName        string     `json:"event_name"`
	SubscriberModule string     `json:"subscriber_module"`
	TenantID         string     `json:"tenant_id"`
	Attempts         int        `json:"attempts"`
	LastAttemptAt    *time.Time `json:"last_attempt_at"`
	LastError        string     `json:"last_error"`
	PayloadPreview   any        `json:"payload_preview"`
	CreatedAt        time.Time  `json:"created_at"`
}

type DLQPage struct {
	Items []DLQItem `json:"items"`
	Total int64     `json:"total"`
}

func (d *DLQ) List(ctx context.Context, filter DLQFilter, limit, offset int) (DLQPage, error) {
	page := DLQPage{Items: []DLQItem{}}
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, fmt.Errorf("begin DLQ snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+d.table+` WHERE `+dlqWhere,
		filter.TenantID, filter.EventName, filter.SubscriberModule).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("count DLQ jobs: %w", err)
	}

	rows, err := tx.Query(ctx, `SELECT id, args, attempt, attempted_at, errors, created_at FROM `+d.table+`
		WHERE `+dlqWhere+` ORDER BY created_at DESC, id DESC LIMIT $4 OFFSET $5`,
		filter.TenantID, filter.EventName, filter.SubscriberModule, limit, offset)
	if err != nil {
		return page, fmt.Errorf("list DLQ jobs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var item DLQItem
		var id int64
		var argsJSON []byte
		var errorJSON [][]byte
		if err := rows.Scan(&id, &argsJSON, &item.Attempts, &item.LastAttemptAt, &errorJSON, &item.CreatedAt); err != nil {
			return page, fmt.Errorf("scan DLQ job: %w", err)
		}

		var args jobqueue.SubscriberDeliveryArgs
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return page, fmt.Errorf("decode DLQ arguments: %w", err)
		}
		item.ID = jobqueue.EncodeJobID(id)
		item.EventID = args.EventID
		item.EventName = args.EventName
		item.SubscriberModule = args.ModuleName
		item.TenantID = args.TenantID
		item.PayloadPreview = dlqPayloadPreview(args.Payload)
		if len(errorJSON) > 0 {
			var last rivertype.AttemptError
			if err := json.Unmarshal(errorJSON[len(errorJSON)-1], &last); err != nil {
				return page, fmt.Errorf("decode DLQ attempt error: %w", err)
			}
			item.LastError = last.Error
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("read DLQ jobs: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return page, fmt.Errorf("commit DLQ snapshot: %w", err)
	}
	return page, nil
}

func dlqPayloadPreview(payload []byte) any {
	if len(payload) > 4096 {
		return map[string]any{"truncated": true, "size_bytes": len(payload)}
	}
	var preview any
	if err := msgpack.Unmarshal(payload, &preview); err != nil {
		return nil
	}
	encoded, err := json.Marshal(preview, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
	if err != nil {
		return nil
	}
	if len(encoded) > 4096 {
		return map[string]any{"truncated": true, "size_bytes": len(payload)}
	}
	return preview
}

// Retry resets only discarded subscriber deliveries; the state predicate makes
// concurrent retry requests claim each job at most once.
func (d *DLQ) Retry(ctx context.Context, filter DLQFilter, id int64) (int64, error) {
	result, err := d.pool.Exec(ctx, `UPDATE `+d.table+` SET state = 'available', queue = 'default',
		attempt = 0, attempted_at = NULL, finalized_at = NULL, scheduled_at = now(),
		errors = '{}', attempted_by = '{}'
		WHERE `+dlqWhere+` AND ($4::bigint = 0 OR id = $4)`,
		filter.TenantID, filter.EventName, filter.SubscriberModule, id)
	if err != nil {
		return 0, fmt.Errorf("retry DLQ jobs: %w", err)
	}
	return result.RowsAffected(), nil
}
