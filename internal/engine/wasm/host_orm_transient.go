package wasm

import (
	"context"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
)

// This file holds host.orm's Transient-model routing (goerp#344) —
// create/read/write/unlink against a Redis-backed key instead of a
// Postgres row. search/search_read reject Transient models outright
// (host_orm.go's makeORMSearch/makeORMSearchRead) — there's no table to
// query.
//
// Deliberately out of scope, same as the Table write path's own
// documented gaps (host_orm_write.go):
//   - Row-level ABAC is a no-op — blocked on a domain-expression
//     *interpreter* (in-memory evaluation against a fetched record) that
//     doesn't exist anywhere in internal/engine/domain (only SQL/RLS-
//     compile targets do; goerp#345 already deferred the identical gap
//     for Virtual models).
//   - Sequence fields — AcquireNext (goerp#340) needs the tenant's
//     Postgres sequences table, which a Transient record never touches.
//   - orm.record.created/updated/deleted event emission — the Table
//     path's "transactional" guarantee is atomicity between a Postgres
//     write and the EventDelivery insert sharing one *sql.Tx; a
//     Transient write has no Postgres transaction to piggyback on, and
//     emitting a best-effort, non-atomic event around it is a real
//     design question this ticket doesn't answer, not something to
//     silently approximate.
//
// The Redis key is tenant-scoped (wizard:{tenant_slug}:{model}:{id}),
// not the wizard:{model}:{id} go-sdk-reference.md §22 originally
// documented — the engine's Redis connection is one shared keyspace
// across every tenant, with no per-tenant database selection the way
// Postgres has per-tenant schemas, so nothing else would stop one
// tenant's Transient record from colliding with or leaking into
// another's. go-sdk-reference.md and implementation-backlog.md have
// been corrected to match.

const (
	transientEtagHashField = "etag"
	transientDataHashField = "data"
)

func transientKey(tenantSlug, qualifiedModel, id string) string {
	return fmt.Sprintf("wizard:%s:%s:%s", tenantSlug, qualifiedModel, id)
}

func transientTTL(md model.ModelDeclaration) time.Duration {
	return time.Duration(md.TransientTTLSeconds) * time.Second
}

// transientCreate assigns a fresh ID if the caller didn't supply one
// (there's no Postgres DEFAULT to fall back on the way Table-backed
// WithStandardFields()'s id column has) and unconditionally creates the
// Redis hash — a fresh key can never collide on etag, so there's no
// precondition to check.
func transientCreate(ctx context.Context, cacheClient *cache.Client, modCtx *ModuleContext, md model.ModelDeclaration, qualifiedModel string, record map[string]any) (ormCreateOutput, *abi.HostError) {
	id, _ := record["id"].(string)
	if id == "" {
		id = uuid.Must(uuid.NewV7()).String()
		record["id"] = id
	}
	etag, _ := record["etag"].(string)
	if hasField(md, "etag") {
		record["etag"] = etag
	}

	data, err := msgpack.Marshal(record)
	if err != nil {
		return ormCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	key := transientKey(modCtx.TenantSlug, qualifiedModel, id)
	if _, err := cacheClient.CompareAndSetHash(ctx, key, transientEtagHashField, false, false, "", transientDataHashField, string(data), etag, transientTTL(md)); err != nil {
		return ormCreateOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	return ormCreateOutput{Record: record}, nil
}

// transientRead supports exactly one ID at a time — a Transient model
// has no List semantics at all (EnableOps(List) is rejected at load
// time), so there is no batch-read use case to support either. An
// expired or never-created key returns orm.not_found, per
// go-sdk-reference.md §22.
func transientRead(ctx context.Context, cacheClient *cache.Client, modCtx *ModuleContext, qualifiedModel string, ids []string) (ormReadOutput, *abi.HostError) {
	if len(ids) != 1 {
		return ormReadOutput{}, &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "Transient models support reading exactly one ID at a time"}
	}

	key := transientKey(modCtx.TenantSlug, qualifiedModel, ids[0])
	fields, found, err := cacheClient.GetHash(ctx, key)
	if err != nil {
		return ormReadOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	if !found {
		return ormReadOutput{}, &abi.HostError{Code: abi.ErrCodeNotFound, Message: "record not found"}
	}

	var record map[string]any
	if err := msgpack.Unmarshal([]byte(fields[transientDataHashField]), &record); err != nil {
		return ormReadOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	return ormReadOutput{Records: []map[string]any{record}}, nil
}

// transientWrite requires the record to already exist — unlike create,
// write must never silently create a record that was never explicitly
// created. requireExists is passed to CompareAndSetHash unconditionally
// (true) rather than checked with a separate GetHash call first: a
// Go-side check-then-CAS would leave a TOCTOU gap a concurrent unlink
// could slip through between the two round trips, resurrecting a
// just-deleted key. checkEtag is only true when the caller actually
// supplied an expectedEtag — an empty expectedEtag means "no
// optimistic-locking precondition," not "the precondition is an empty
// string" (a legitimately stored etag can itself be "").
func transientWrite(ctx context.Context, cacheClient *cache.Client, modCtx *ModuleContext, md model.ModelDeclaration, qualifiedModel, id string, record map[string]any, newEtag, expectedEtag string) (ormWriteOutput, *abi.HostError) {
	key := transientKey(modCtx.TenantSlug, qualifiedModel, id)

	data, err := msgpack.Marshal(record)
	if err != nil {
		return ormWriteOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	ok, err := cacheClient.CompareAndSetHash(ctx, key, transientEtagHashField, true, expectedEtag != "", expectedEtag, transientDataHashField, string(data), newEtag, transientTTL(md))
	if err != nil {
		return ormWriteOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	if !ok {
		return ormWriteOutput{}, diagnoseTransientZeroRowWrite(ctx, cacheClient, key)
	}

	return ormWriteOutput{Record: record}, nil
}

// diagnoseTransientZeroRowWrite disambiguates a failed CompareAndSetHash
// the same way diagnoseZeroRowWrite does for the Table path: re-checking
// existence tells a stale etag (found, orm.etag_mismatch) apart from a
// missing or already-expired key (orm.not_found).
func diagnoseTransientZeroRowWrite(ctx context.Context, cacheClient *cache.Client, key string) *abi.HostError {
	_, found, err := cacheClient.GetHash(ctx, key)
	if err != nil {
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	if found {
		return &abi.HostError{Code: abi.ErrCodeEtagMismatch, Message: "record has been modified since it was last read"}
	}
	return &abi.HostError{Code: abi.ErrCodeNotFound, Message: "record not found"}
}

func transientUnlink(ctx context.Context, cacheClient *cache.Client, modCtx *ModuleContext, qualifiedModel, id string) (ormUnlinkOutput, *abi.HostError) {
	key := transientKey(modCtx.TenantSlug, qualifiedModel, id)

	_, found, err := cacheClient.GetHash(ctx, key)
	if err != nil {
		return ormUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	if !found {
		return ormUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeNotFound, Message: "record not found"}
	}

	if err := cacheClient.Delete(ctx, key); err != nil {
		return ormUnlinkOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	return ormUnlinkOutput{Deleted: true}, nil
}
