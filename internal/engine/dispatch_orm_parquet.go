package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/rs/zerolog/log"
)

// parquetContentType is view-system.md §8's documented content type for a
// use_wasm:true pivot view's underlying list response.
const parquetContentType = "application/vnd.apache.parquet"

// wantsParquet reports whether a list request asked for the Parquet
// encoding via ?format=parquet or Accept: application/vnd.apache.parquet
// (view-system.md §8).
func wantsParquet(r *http.Request) bool {
	if r.URL.Query().Get("format") == "parquet" {
		return true
	}
	return r.Header.Get("Accept") == parquetContentType
}

// writeParquet encodes records as a Parquet file and writes it with
// view-system.md §8's documented content type. The schema is the sorted
// union of keys across records, not any caller-supplied column list — safe
// because applyFieldMasking/expandRelations (host_orm.go) both apply
// uniformly per field across a whole request, so every record in one
// response carries the same key set. A value scanRowsToMaps/expandRelations
// doesn't produce as one of Go's own bool/int64/float64/string/[]byte/
// time.Time/nil (an expanded relation's {id, display_name} object, chiefly)
// is JSON-encoded into a string column instead of a nested Parquet struct —
// this ticket's actual consumer (DuckDB-WASM pivot re-aggregation) groups
// by flat dimension/measure columns, not nested objects.
func writeParquet(w http.ResponseWriter, records []map[string]any) {
	normalized := make([]map[string]any, len(records))
	for i, record := range records {
		row := make(map[string]any, len(record))
		for k, v := range record {
			row[k] = normalizeParquetValue(v)
		}
		normalized[i] = row
	}

	columns := parquetColumns(normalized)
	schema := parquet.NewSchema("record", parquetGroup(columns, normalized))

	var buf bytes.Buffer
	pw := parquet.NewWriter(&buf, schema)
	for _, row := range normalized {
		if err := pw.Write(row); err != nil {
			log.Error().Err(err).Msg("dispatchORMList: encode parquet row")
			writeRouteError(w, http.StatusInternalServerError, "internal", "failed to encode response")
			return
		}
	}
	if err := pw.Close(); err != nil {
		log.Error().Err(err).Msg("dispatchORMList: close parquet writer")
		writeRouteError(w, http.StatusInternalServerError, "internal", "failed to encode response")
		return
	}

	w.Header().Set("Content-Type", parquetContentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(buf.Bytes()); err != nil {
		log.Error().Err(err).Msg("dispatchORMList: write parquet response")
	}
}

// parquetColumns returns the sorted union of keys across records — sorted
// so column order is deterministic across requests with the same field
// set, not dependent on Go's randomized map iteration.
func parquetColumns(records []map[string]any) []string {
	seen := make(map[string]struct{})
	for _, record := range records {
		for k := range record {
			seen[k] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// parquetGroup builds a dynamic schema, one optional leaf per column —
// optional even for a column with no observed nil, since a Parquet
// column's nullability is fixed at the schema level, not inferred
// per-write.
func parquetGroup(columns []string, records []map[string]any) parquet.Group {
	group := make(parquet.Group, len(columns))
	for _, col := range columns {
		group[col] = parquetNodeFor(col, records)
	}
	return group
}

// parquetNodeFor types a column from its first non-nil value across
// records; an all-nil column defaults to a nullable string, the safest
// fallback with no sample to type from.
func parquetNodeFor(col string, records []map[string]any) parquet.Node {
	for _, record := range records {
		v, ok := record[col]
		if !ok || v == nil {
			continue
		}
		switch v.(type) {
		case bool:
			return parquet.Optional(parquet.Leaf(parquet.BooleanType))
		case int64:
			return parquet.Optional(parquet.Leaf(parquet.Int64Type))
		case float64:
			return parquet.Optional(parquet.Leaf(parquet.DoubleType))
		case time.Time:
			return parquet.Optional(parquet.Timestamp(parquet.Microsecond))
		default:
			return parquet.Optional(parquet.String())
		}
	}
	return parquet.Optional(parquet.String())
}

// normalizeParquetValue reduces scanRowsToMaps'/expandRelations' possible
// value shapes down to what parquetNodeFor above switches on: nil, bool,
// int64, float64, time.Time, or string. []byte (bytea, and how NUMERIC/
// DECIMAL columns come back through database/sql's generic driver.Value
// conversion) becomes its UTF-8 string; anything else (an expanded
// relation's map, chiefly) is JSON-encoded, falling back to fmt.Sprintf if
// that ever fails.
func normalizeParquetValue(v any) any {
	switch val := v.(type) {
	case nil, bool, int64, float64, string, time.Time:
		return val
	case []byte:
		return string(val)
	default:
		encoded, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%v", val)
		}
		return string(encoded)
	}
}
