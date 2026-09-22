package abi

// Wire types of the host.orm namespace (host-abi-reference.md §5a) and of
// the module exports the engine calls on behalf of host.orm: computed
// fields, constraint hooks and preview hooks. Request members the SDK does
// not set are omitempty, so the bytes it sends are the same whether or not
// the engine accepts the member.

// ORMSearchInput is the request of host.orm.search.
type ORMSearchInput struct {
	Model  string `msgpack:"model"`
	Domain string `msgpack:"domain"`
	Order  string `msgpack:"order,omitempty"`
	Limit  int    `msgpack:"limit,omitempty"`
	Offset int    `msgpack:"offset,omitempty"` // the SDK pages with a cursor and never sets it
	TxID   string `msgpack:"tx_id"`
}

// ORMSearchOutput is the response of host.orm.search.
type ORMSearchOutput struct {
	IDs   []string `msgpack:"ids"`
	Count int64    `msgpack:"count"`
}

// ORMSearchReadInput is the request of host.orm.search_read.
type ORMSearchReadInput struct {
	Model  string   `msgpack:"model"`
	Domain string   `msgpack:"domain"`
	Fields []string `msgpack:"fields,omitempty"`
	Order  string   `msgpack:"order,omitempty"`
	Limit  int      `msgpack:"limit,omitempty"`
	Offset int      `msgpack:"offset,omitempty"` // the SDK pages with Cursor and never sets it
	Cursor string   `msgpack:"cursor,omitempty"`
	TxID   string   `msgpack:"tx_id"`
}

// ORMSearchReadOutput is the response of host.orm.search_read.
type ORMSearchReadOutput struct {
	Records    []map[string]any `msgpack:"records"`
	NextCursor string           `msgpack:"next_cursor,omitempty"`
}

// ORMReadInput is the request of host.orm.read.
type ORMReadInput struct {
	Model  string   `msgpack:"model"`
	IDs    []string `msgpack:"ids"`
	Fields []string `msgpack:"fields,omitempty"`
	TxID   string   `msgpack:"tx_id"`
}

// ORMReadOutput is the response of host.orm.read.
type ORMReadOutput struct {
	Records []map[string]any `msgpack:"records"`
}

// ORMOnConflict is the conflict policy of host.orm.create and
// host.orm.create_batch.
type ORMOnConflict struct {
	Fields []string `msgpack:"fields"`
	Policy string   `msgpack:"policy"` // "ignore" | "update"
}

// ORMCreateInput is the request of host.orm.create.
type ORMCreateInput struct {
	Model      string         `msgpack:"model"`
	Record     map[string]any `msgpack:"record"`
	OnConflict *ORMOnConflict `msgpack:"on_conflict,omitempty"`
	TxID       string         `msgpack:"tx_id"`
}

// ORMCreateOutput is the response of host.orm.create.
type ORMCreateOutput struct {
	Record map[string]any `msgpack:"record"`
}

// ORMCreateBatchInput is the request of host.orm.create_batch.
type ORMCreateBatchInput struct {
	Model      string           `msgpack:"model"`
	Records    []map[string]any `msgpack:"records"`
	OnConflict *ORMOnConflict   `msgpack:"on_conflict,omitempty"`
	TxID       string           `msgpack:"tx_id"`
}

// ORMCreateBatchOutput is the response of host.orm.create_batch.
type ORMCreateBatchOutput struct {
	Records []map[string]any `msgpack:"records"`
}

// ORMFirstOrCreateInput is the request of host.orm.first_or_create.
type ORMFirstOrCreateInput struct {
	Model      string         `msgpack:"model"`
	UniqueVals map[string]any `msgpack:"unique_vals"`
	CreateVals map[string]any `msgpack:"create_vals"`
	TxID       string         `msgpack:"tx_id"`
}

// ORMFirstOrCreateOutput is the response of host.orm.first_or_create.
type ORMFirstOrCreateOutput struct {
	Record  map[string]any `msgpack:"record"`
	Created bool           `msgpack:"created"`
}

// ORMWriteInput is the request of host.orm.write. ExpectedEtag is nil when
// the caller supplied no optimistic-locking precondition, which differs from
// a pointer to "": a precondition requiring the stored etag to still be its
// never-written default. A record no write has touched keeps that empty
// default, so a bare string could not express a precondition on it.
type ORMWriteInput struct {
	Model        string         `msgpack:"model"`
	ID           string         `msgpack:"id"`
	Record       map[string]any `msgpack:"record"`
	ExpectedEtag *string        `msgpack:"expected_etag,omitempty"`
	TxID         string         `msgpack:"tx_id"`
}

// ORMWriteOutput is the response of host.orm.write.
type ORMWriteOutput struct {
	Record map[string]any `msgpack:"record"`
}

// ORMWriteManyInput is the request of host.orm.write_many.
type ORMWriteManyInput struct {
	Model  string         `msgpack:"model"`
	IDs    []string       `msgpack:"ids"`
	Record map[string]any `msgpack:"record"`
	TxID   string         `msgpack:"tx_id"`
}

// ORMWriteWhereInput is the request of host.orm.write_where.
type ORMWriteWhereInput struct {
	Model  string         `msgpack:"model"`
	Domain string         `msgpack:"domain"`
	Record map[string]any `msgpack:"record"`
	TxID   string         `msgpack:"tx_id"`
}

// ORMMutateOp is one relative numeric change of host.orm.mutate: Delta is
// added to Field, so a decrement carries a negative Delta.
type ORMMutateOp struct {
	Field string `msgpack:"field"`
	Delta any    `msgpack:"delta"`
}

// ORMMutateInput is the request of host.orm.mutate. Guard is a domain
// expression evaluated against the record as it stands immediately before
// the change; empty means unconditional.
type ORMMutateInput struct {
	Model string        `msgpack:"model"`
	ID    string        `msgpack:"id"`
	Ops   []ORMMutateOp `msgpack:"ops"`
	Guard string        `msgpack:"guard,omitempty"`
	TxID  string        `msgpack:"tx_id"`
}

// ORMMutateOutput is the response of host.orm.mutate.
type ORMMutateOutput struct {
	Record map[string]any `msgpack:"record"`
}

// ORMExecResult is the response of host.orm.write_many and
// host.orm.write_where: how many rows changed and which ones, without
// returning every full record.
type ORMExecResult struct {
	Count int      `msgpack:"count"`
	IDs   []string `msgpack:"ids"`
}

// ORMRecordCreatedPayload is the payload of orm.record.created. Record is
// set for a single create; Records instead for a create_batch batch, one
// event for the whole batch. Mutually exclusive.
type ORMRecordCreatedPayload struct {
	Model   string           `msgpack:"model"`
	Record  map[string]any   `msgpack:"record,omitempty"`
	Records []map[string]any `msgpack:"records,omitempty"`
}

// ORMRecordUpdatedPayload is the payload of orm.record.updated — always
// one record. ChangedFields lists the fields the call actually wrote,
// sorted, excluding engine-managed columns.
type ORMRecordUpdatedPayload struct {
	Model         string         `msgpack:"model"`
	Record        map[string]any `msgpack:"record"`
	ChangedFields []string       `msgpack:"changed_fields"`
}

// ORMRecordDeletedPayload is the payload of orm.record.deleted, one per
// deleted ID. Record holds only the deleted id.
type ORMRecordDeletedPayload struct {
	Model  string         `msgpack:"model"`
	Record map[string]any `msgpack:"record"`
}

// ORMAggregateValue is one {field, aggregation} pair of host.orm.aggregate.
// Field is empty only for "count", which then counts every row the
// domain and the caller's row-level security admit rather than a
// specific column's non-null values.
type ORMAggregateValue struct {
	Field       string `msgpack:"field,omitempty"`
	Aggregation string `msgpack:"aggregation"`
}

// ORMAggregateInput is the request of host.orm.aggregate.
type ORMAggregateInput struct {
	Model  string              `msgpack:"model"`
	Domain string              `msgpack:"domain,omitempty"`
	Values []ORMAggregateValue `msgpack:"values"`
	TxID   string              `msgpack:"tx_id"`
}

// ORMAggregateOutput is the response of host.orm.aggregate: the ungrouped
// grand total for each requested value, keyed by "<field>_<aggregation>"
// ("_count" when Field is empty).
type ORMAggregateOutput struct {
	Values map[string]any `msgpack:"values"`
}

// ORMUnlinkInput is the request of host.orm.unlink.
type ORMUnlinkInput struct {
	Model string   `msgpack:"model"`
	IDs   []string `msgpack:"ids"`
	TxID  string   `msgpack:"tx_id"`
}

// ComputeRequest is what the engine sends a module's compute export to
// recompute one computed field.
type ComputeRequest struct {
	FnName   string         `msgpack:"fn_name"`
	Record   map[string]any `msgpack:"record"`
	TenantID string         `msgpack:"tenant_id,omitempty"`
	UserID   string         `msgpack:"user_id,omitempty"`
	TraceID  string         `msgpack:"trace_id,omitempty"`
}

// ComputeResponse is what a module's compute export returns.
type ComputeResponse struct {
	Value any           `msgpack:"value,omitempty"`
	Error *ComputeError `msgpack:"error,omitempty"`
}

// ComputeError is a compute function's failure.
type ComputeError struct {
	Code    string `msgpack:"code"`
	Message string `msgpack:"message"`
}

// ConstraintRequest is what the engine sends a module's constraint export.
// Phase is "create", "write" or "delete".
type ConstraintRequest struct {
	Model    string         `msgpack:"model"`
	Phase    string         `msgpack:"phase"`
	Record   map[string]any `msgpack:"record"`
	TenantID string         `msgpack:"tenant_id,omitempty"`
	UserID   string         `msgpack:"user_id,omitempty"`
	TraceID  string         `msgpack:"trace_id,omitempty"`
}

// ConstraintResponse is what a module's constraint export returns.
type ConstraintResponse struct {
	Allowed bool             `msgpack:"allowed"`
	Field   string           `msgpack:"field,omitempty"`
	Message string           `msgpack:"message,omitempty"`
	Error   *ConstraintError `msgpack:"error,omitempty"`
}

// ConstraintError is a constraint hook's failure.
type ConstraintError struct {
	Code    string `msgpack:"code"`
	Message string `msgpack:"message"`
}

// PreviewRequest is what the engine sends a module's preview export.
type PreviewRequest struct {
	Model    string         `msgpack:"model"`
	Record   map[string]any `msgpack:"record"`
	TenantID string         `msgpack:"tenant_id,omitempty"`
	UserID   string         `msgpack:"user_id,omitempty"`
	TraceID  string         `msgpack:"trace_id,omitempty"`
}

// PreviewResponse is what a module's preview export returns.
type PreviewResponse struct {
	Record map[string]any `msgpack:"record,omitempty"`
	Error  *PreviewError  `msgpack:"error,omitempty"`
}

// PreviewError is a preview hook's failure.
type PreviewError struct {
	Code    string `msgpack:"code"`
	Message string `msgpack:"message"`
}

// VirtualOpRequest is what the engine sends a module's handle_virtual_op
// export for a model backed by a Virtual backend.
type VirtualOpRequest struct {
	Model        string         `msgpack:"model"`
	Op           string         `msgpack:"op"`
	ID           string         `msgpack:"id,omitempty"`
	Record       map[string]any `msgpack:"record,omitempty"`
	ExpectedEtag string         `msgpack:"expected_etag,omitempty"`
	Limit        int            `msgpack:"limit,omitempty"`
	Offset       int            `msgpack:"offset,omitempty"`
	TenantID     string         `msgpack:"tenant_id,omitempty"`
	UserID       string         `msgpack:"user_id,omitempty"`
	TraceID      string         `msgpack:"trace_id,omitempty"`
}

// VirtualOpResponse is what a module's handle_virtual_op export returns.
type VirtualOpResponse struct {
	Record  map[string]any   `msgpack:"record,omitempty"`
	Records []map[string]any `msgpack:"records,omitempty"`
	Error   *VirtualOpError  `msgpack:"error,omitempty"`
}

// VirtualOpError is a Virtual backend function's failure.
type VirtualOpError struct {
	Code    string `msgpack:"code"`
	Message string `msgpack:"message"`
}
