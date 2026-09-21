package abi

// Wire types of the host.db namespace (host-abi-reference.md §5). Request
// members the SDK does not set are omitempty, so the bytes it sends are the
// same whether or not the engine accepts the member.

// DBBeginInput is the request of host.db.begin.
type DBBeginInput struct {
	Isolation string `msgpack:"isolation"`
	ReadOnly  bool   `msgpack:"read_only"`
}

// DBBeginOutput is the response of host.db.begin.
type DBBeginOutput struct {
	TxID      string `msgpack:"tx_id"`
	ExpiresAt int64  `msgpack:"expires_at"`
}

// DBTxIDInput is the request of host.db.commit and host.db.rollback.
type DBTxIDInput struct {
	TxID string `msgpack:"tx_id"`
}

// DBDurationOutput is the response of host.db.commit and host.db.rollback.
type DBDurationOutput struct {
	DurationMs float64 `msgpack:"duration_ms"`
}

// DBLockInput is the request of host.db.lock.
type DBLockInput struct {
	Key       string `msgpack:"key"`
	TxID      string `msgpack:"tx_id"`
	TimeoutMs int64  `msgpack:"timeout_ms"`
	Shared    bool   `msgpack:"shared,omitempty"`
}

// DBLockOutput is the response of host.db.lock.
type DBLockOutput struct {
	Acquired   bool    `msgpack:"acquired"`
	DurationMs float64 `msgpack:"duration_ms"`
}

// DBNotifyInput is the request of host.db.notify.
type DBNotifyInput struct {
	Channel string `msgpack:"channel"`
	Payload string `msgpack:"payload"`
	TxID    string `msgpack:"tx_id,omitempty"`
}

// DBQueryOpts holds the options of host.db.query and host.db.query_replica.
type DBQueryOpts struct {
	TimeoutMs int64 `msgpack:"timeout_ms"`
	ReadOnly  bool  `msgpack:"read_only"`
}

// DBQueryInput is the request of host.db.query and host.db.query_replica.
type DBQueryInput struct {
	SQL    string      `msgpack:"sql"`
	Params []any       `msgpack:"params"`
	TxID   string      `msgpack:"tx_id"`
	Opts   DBQueryOpts `msgpack:"opts"`
}

// DBQueryOutput is the response of host.db.query and host.db.query_replica.
type DBQueryOutput struct {
	Rows         [][]any  `msgpack:"rows"`
	ColumnNames  []string `msgpack:"column_names"`
	RowsAffected int      `msgpack:"rows_affected"`
	DurationMs   float64  `msgpack:"duration_ms"`
}

// DBExecOpts holds the options of host.db.exec.
type DBExecOpts struct {
	TimeoutMs  int64  `msgpack:"timeout_ms,omitempty"`
	Returning  string `msgpack:"returning,omitempty"`
	SkipAudit  bool   `msgpack:"skip_audit,omitempty"`
	SkipEtag   bool   `msgpack:"skip_etag,omitempty"`
	ExpectRows bool   `msgpack:"expect_rows,omitempty"`
}

// DBExecInput is the request of host.db.exec.
type DBExecInput struct {
	SQL    string     `msgpack:"sql"`
	Params []any      `msgpack:"params"`
	TxID   string     `msgpack:"tx_id,omitempty"`
	Opts   DBExecOpts `msgpack:"opts"`
}

// DBExecOutput is the response of host.db.exec.
type DBExecOutput struct {
	RowsAffected int     `msgpack:"rows_affected"`
	Returning    [][]any `msgpack:"returning,omitempty"`
	DurationMs   float64 `msgpack:"duration_ms"`
}

// DBExecBatchOpts holds the options of host.db.exec_batch.
type DBExecBatchOpts struct {
	ContinueOnError bool   `msgpack:"continue_on_error"`
	TimeoutMs       int64  `msgpack:"timeout_ms,omitempty"`
	Returning       string `msgpack:"returning,omitempty"`
	SkipAudit       bool   `msgpack:"skip_audit,omitempty"`
	SkipEtag        bool   `msgpack:"skip_etag,omitempty"`
}

// DBExecBatchInput is the request of host.db.exec_batch.
type DBExecBatchInput struct {
	SQL       string          `msgpack:"sql"`
	ParamSets [][]any         `msgpack:"param_sets"`
	TxID      string          `msgpack:"tx_id,omitempty"`
	Opts      DBExecBatchOpts `msgpack:"opts"`
}

// DBBatchRowError is one failed parameter set in the details of a
// db.batch_partial_error.
type DBBatchRowError struct {
	Index   int            `msgpack:"index"`
	Code    string         `msgpack:"code"`
	Message string         `msgpack:"message"`
	Details map[string]any `msgpack:"details,omitempty"`
}

// DBExecBatchOutput is the response of host.db.exec_batch on a fully
// successful batch. A partial failure is a db.batch_partial_error whose
// details carry total_rows_affected, failed_count, errors and returning.
type DBExecBatchOutput struct {
	TotalRowsAffected int     `msgpack:"total_rows_affected"`
	Returning         [][]any `msgpack:"returning,omitempty"`
	DurationMs        float64 `msgpack:"duration_ms"`
}

// Operations of host.db.migration_ddl.
const (
	DBMigrationDDLOpDropColumn = "drop_column"
	DBMigrationDDLOpDropTable  = "drop_table"
)

// DBMigrationDDLInput is the request of host.db.migration_ddl.
type DBMigrationDDLInput struct {
	Op     string `msgpack:"op"`
	Table  string `msgpack:"table"`
	Column string `msgpack:"column,omitempty"`
}

// DBMigrationDDLOutput is the response of host.db.migration_ddl.
type DBMigrationDDLOutput struct {
	DurationMs float64 `msgpack:"duration_ms"`
}
