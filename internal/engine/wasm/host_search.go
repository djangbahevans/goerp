package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// searchScoreAlias is host.search.query's own internal column alias for
// the trigram similarity score — never user/manifest-supplied, so it
// doesn't need quoteIdentORM's sanitization, and stripped from every hit
// before it's returned (it isn't part of any model's declared fields).
const searchScoreAlias = "search_score"

// defaultSearchLimit/maxSearchLimit match host-abi-reference.md §12's
// documented opts.limit default/max.
const (
	defaultSearchLimit = 20
	maxSearchLimit     = 1000
)

// registerHostSearch attaches host.search.query/update/delete to the
// runtime (host-abi-reference.md §12). query is backed by Postgres
// trigram similarity — the initial-build backend (data-layer.md §5);
// update/delete are Meilisearch-only and always report abi.unavailable
// until Meilisearch is introduced, since the trigram backend queries
// table rows directly and has no separate index to maintain.
func registerHostSearch(ctx context.Context, rt wazero.Runtime, r *Runtime, db *sql.DB) error {
	_, err := rt.NewHostModuleBuilder("host.search").
		NewFunctionBuilder().WithFunc(makeSearchQuery(r, db)).Export("query").
		NewFunctionBuilder().WithFunc(makeSearchUpdate(r)).Export("update").
		NewFunctionBuilder().WithFunc(makeSearchDelete(r)).Export("delete").
		Instantiate(ctx)
	return err
}

type SearchQueryOpts struct {
	Filter string   `msgpack:"filter,omitempty"`
	Sort   []string `msgpack:"sort,omitempty"`
	Limit  int      `msgpack:"limit,omitempty"`
	Offset int      `msgpack:"offset,omitempty"`
	Facets []string `msgpack:"facets,omitempty"`
}

type SearchQueryInput struct {
	Index string          `msgpack:"index"`
	Query string          `msgpack:"query"`
	Opts  SearchQueryOpts `msgpack:"opts"`
}

type SearchQueryOutput struct {
	Hits              []map[string]any          `msgpack:"hits"`
	TotalHits         int64                     `msgpack:"total_hits"`
	ProcessingTimeMs  int                       `msgpack:"processing_time_ms"`
	FacetDistribution map[string]map[string]int `msgpack:"facet_distribution,omitempty"`
}

func makeSearchQuery(r *Runtime, db *sql.DB) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input SearchQueryInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		out, hostErr := SearchQuery(ctx, db, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, out)
	}
}

// SearchQuery is host.search.query's plain-Go core, callable without a
// WASM instance in the loop — same shared-entry-point shape ORMSearch/
// ORMSearchRead (host_orm.go) use. Implements data-layer.md §5.4's
// trigram query shape: similarity-ranked results from the index's first
// declared Searchable field, tenant-scoped through the same
// beginTenantScopedRead transaction host.orm's own reads use (RLS/
// search_path, not a manual tenant_id filter — this codebase's real
// multitenancy layer, not data-layer.md §5.4's own tenant_id-column
// pseudocode, which predates it).
func SearchQuery(ctx context.Context, db *sql.DB, modCtx *ModuleContext, input SearchQueryInput) (SearchQueryOutput, *abi.HostError) {
	if !modCtx.Capabilities().Has(abi.CapSearchQuery) {
		return SearchQueryOutput{}, abi.CapabilityDenied("search.query")
	}

	searchIndexReg := modCtx.SearchIndexRegistry()
	if searchIndexReg == nil {
		return SearchQueryOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: "no search index registry available"}
	}
	idx, ok := searchIndexReg.Index(modCtx.ModuleName, input.Index)
	if !ok {
		return SearchQueryOutput{}, &abi.HostError{Code: abi.ErrCodeIndexNotFound, Message: "search index " + input.Index + " is not declared by this module"}
	}
	if len(idx.Searchable) == 0 {
		return SearchQueryOutput{}, &abi.HostError{Code: abi.ErrCodeIndexNotFound, Message: "search index " + input.Index + " declares no searchable fields"}
	}

	limit := input.Opts.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	tx, err := beginTenantScopedRead(ctx, db, modCtx)
	if err != nil {
		return SearchQueryOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	defer func() { _ = tx.Rollback() }()

	table := quoteIdentORM(idx.Table)
	primaryField := quoteIdentORM(idx.Searchable[0])

	whereClauses := []string{fmt.Sprintf("%s %% $1", primaryField)}
	if idx.SoftDeleteField != "" {
		whereClauses = append(whereClauses, quoteIdentORM(idx.SoftDeleteField)+" IS NULL")
	}
	whereFrag := strings.Join(whereClauses, " AND ")

	var totalHits int64
	countSQL := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", table, whereFrag)
	if err := tx.QueryRowContext(ctx, countSQL, input.Query).Scan(&totalHits); err != nil {
		return SearchQueryOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	displayCols := make([]string, len(idx.Displayed))
	for i, c := range idx.Displayed {
		displayCols[i] = quoteIdentORM(c)
	}
	selectCols := strings.Join(displayCols, ", ")
	if selectCols == "" {
		selectCols = "*"
	}

	listSQL := fmt.Sprintf(
		"SELECT %s, similarity(%s, $1) AS %s FROM %s WHERE %s ORDER BY %s DESC LIMIT $2 OFFSET $3",
		selectCols, primaryField, searchScoreAlias, table, whereFrag, searchScoreAlias,
	)
	rows, err := tx.QueryContext(ctx, listSQL, input.Query, limit, input.Opts.Offset)
	if err != nil {
		return SearchQueryOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	defer rows.Close()

	hits, err := scanRowsToMaps(rows)
	if err != nil {
		return SearchQueryOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	for _, hit := range hits {
		delete(hit, searchScoreAlias)
	}

	return SearchQueryOutput{Hits: hits, TotalHits: totalHits}, nil
}

// searchUnavailableError is returned by both host.search.update and
// host.search.delete — Meilisearch-only calls against the trigram-only
// initial build (host-abi-reference.md §12, corrected).
func searchUnavailableError(op string) *abi.HostError {
	return &abi.HostError{
		Code:    abi.ErrCodeUnavailable,
		Message: "host.search." + op + " is only meaningful once Meilisearch is introduced (data-layer.md §5) — the trigram backend queries table rows directly and has no separate index to " + op,
	}
}

func makeSearchUpdate(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate
		if !modCtx.Capabilities().Has(abi.CapSearchIndex) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("search.index"))
		}
		return abi.EncodeHostError(ctx, m, allocate, searchUnavailableError("update"))
	}
}

func makeSearchDelete(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate
		if !modCtx.Capabilities().Has(abi.CapSearchIndex) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("search.index"))
		}
		return abi.EncodeHostError(ctx, m, allocate, searchUnavailableError("delete"))
	}
}
