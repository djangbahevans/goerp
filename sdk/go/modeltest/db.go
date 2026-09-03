package modeltest

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// TestDB is h.DB — seeding and asserting against the harness's tenant
// schema (§5, §8 "Database state"). Every method runs directly against
// real Postgres; there is no mock mode in this build (testing-guide.md
// §2, "the initial build targets the harness running against Postgres...
// only").
type TestDB struct {
	t          *testing.T
	db         *sql.DB
	tenantID   string
	tenantSlug string
}

func newTestDB(t *testing.T, db *sql.DB, tenantID, tenantSlug string) *TestDB {
	return &TestDB{t: t, db: db, tenantID: tenantID, tenantSlug: tenantSlug}
}

func (d *TestDB) schema() string { return tenantschema.Name(d.tenantSlug) }

// Seed inserts one record (a map[string]any) or several
// ([]map[string]any) into table, in the harness's tenant schema.
// tenant_id is injected automatically and overwritten if present.
func (d *TestDB) Seed(table string, records any) {
	d.t.Helper()
	rows := toRecordSlice(d.t, records)
	for _, row := range rows {
		d.insert(table, row)
	}
}

// SeedSystem inserts one record (or several) into table in the shared
// system schema, for cross-tenant lookups or user-management fixtures —
// unlike Seed, no tenant_id is injected.
func (d *TestDB) SeedSystem(table string, records any) {
	d.t.Helper()
	rows := toRecordSlice(d.t, records)
	for _, row := range rows {
		d.insertInto(`"system"`, table, row)
	}
}

func (d *TestDB) insert(table string, row map[string]any) {
	d.t.Helper()
	cloned := make(map[string]any, len(row)+1)
	for k, v := range row {
		cloned[k] = v
	}
	cloned["tenant_id"] = d.tenantID
	d.insertInto(d.schema(), table, cloned)
}

func toRecordSlice(t *testing.T, records any) []map[string]any {
	t.Helper()
	switch v := records.(type) {
	case map[string]any:
		return []map[string]any{v}
	case []map[string]any:
		return v
	default:
		t.Fatalf("modeltest: Seed/SeedSystem expects map[string]any or []map[string]any, got %T", records)
		return nil
	}
}

func (d *TestDB) insertInto(schema, table string, row map[string]any) {
	d.t.Helper()

	cols := make([]string, 0, len(row))
	for c := range row {
		cols = append(cols, c)
	}
	sort.Strings(cols)

	placeholders := make([]string, len(cols))
	args := make([]any, len(cols))
	quotedCols := make([]string, len(cols))
	for i, c := range cols {
		quotedCols[i] = `"` + c + `"`
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = row[c]
	}

	query := fmt.Sprintf(`INSERT INTO %s.%q (%s) VALUES (%s)`,
		schema, table, strings.Join(quotedCols, ", "), strings.Join(placeholders, ", "))
	if _, err := d.db.Exec(query, args...); err != nil {
		d.t.Fatalf("modeltest: seed %s.%s: %v", schema, table, err)
	}
}

// SeedFromFixture seeds the harness's tenant schema from a JSON fixture
// file shaped { "table_name": [ {...}, {...} ], ... } (§5).
func (d *TestDB) SeedFromFixture(path string) {
	d.t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		d.t.Fatalf("modeltest: read fixture %s: %v", path, err)
	}
	var fixture map[string][]map[string]any
	if err := json.Unmarshal(raw, &fixture); err != nil {
		d.t.Fatalf("modeltest: parse fixture %s: %v", path, err)
	}
	tables := make([]string, 0, len(fixture))
	for table := range fixture {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		for _, row := range fixture[table] {
			d.insert(table, row)
		}
	}
}

// AssertExists fails the test unless table has a row matching every
// key/value in where.
func (d *TestDB) AssertExists(table string, where map[string]any) {
	d.t.Helper()
	n := d.countWhere(table, where)
	if n == 0 {
		d.t.Fatalf("modeltest: expected a row in %s matching %v, found none", table, where)
	}
}

// AssertNotExists fails the test if table has any row matching every
// key/value in where.
func (d *TestDB) AssertNotExists(table string, where map[string]any) {
	d.t.Helper()
	n := d.countWhere(table, where)
	if n != 0 {
		d.t.Fatalf("modeltest: expected no row in %s matching %v, found %d", table, where, n)
	}
}

func (d *TestDB) countWhere(table string, where map[string]any) int {
	d.t.Helper()
	cols := make([]string, 0, len(where))
	for c := range where {
		cols = append(cols, c)
	}
	sort.Strings(cols)

	conds := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, c := range cols {
		conds[i] = fmt.Sprintf("%q = $%d", c, i+1)
		args[i] = where[c]
	}
	whereClause := "TRUE"
	if len(conds) > 0 {
		whereClause = strings.Join(conds, " AND ")
	}

	query := fmt.Sprintf(`SELECT count(*) FROM %s.%q WHERE %s`, d.schema(), table, whereClause)
	var n int
	if err := d.db.QueryRow(query, args...).Scan(&n); err != nil {
		d.t.Fatalf("modeltest: query %s: %v", table, err)
	}
	return n
}

// AssertCount fails the test unless table has exactly want rows matching
// the raw SQL condition in where (interpolated as-is after WHERE — not
// parameterized, matching testing-guide.md §8's own literal-value usage).
func (d *TestDB) AssertCount(table string, want int, where string) {
	d.t.Helper()
	if where == "" {
		where = "TRUE"
	}
	query := fmt.Sprintf(`SELECT count(*) FROM %s.%q WHERE %s`, d.schema(), table, where)
	var n int
	if err := d.db.QueryRow(query).Scan(&n); err != nil {
		d.t.Fatalf("modeltest: query %s: %v", table, err)
	}
	if n != want {
		d.t.Fatalf("modeltest: %s count = %d, want %d (where %s)", table, n, want, where)
	}
}

// QueryOne runs query (with args) against the tenant schema and scans the
// single resulting row into dest, a pointer to a plain struct — column
// values are matched to dest's fields by a `db:"..."` tag, or by
// snake-casing the field's own name when untagged, the same convention
// sdk/go/db uses for its own struct mapping (go-sdk-reference.md §6
// "Struct mapping").
func (d *TestDB) QueryOne(dest any, query string, args ...any) {
	d.t.Helper()

	tx, err := d.db.Begin()
	if err != nil {
		d.t.Fatalf("modeltest: QueryOne begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(fmt.Sprintf(`SET LOCAL search_path = %s`, d.schema())); err != nil {
		d.t.Fatalf("modeltest: QueryOne set search_path: %v", err)
	}

	rows, err := tx.Query(query, args...)
	if err != nil {
		d.t.Fatalf("modeltest: QueryOne: %v", err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		d.t.Fatalf("modeltest: QueryOne columns: %v", err)
	}
	targets, err := scanTargets(dest, cols)
	if err != nil {
		d.t.Fatalf("modeltest: QueryOne: %v", err)
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			d.t.Fatalf("modeltest: QueryOne: %v", err)
		}
		d.t.Fatalf("modeltest: QueryOne: query returned no rows")
		return
	}
	if err := rows.Scan(targets...); err != nil {
		d.t.Fatalf("modeltest: QueryOne scan: %v", err)
	}
}

// scanTargets returns, for each of cols in order, a pointer into dest's
// matching field — dest must be a non-nil pointer to a struct.
func scanTargets(dest any, cols []string) ([]any, error) {
	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("dest must be a non-nil pointer to a struct, got %T", dest)
	}
	elem := v.Elem()
	t := elem.Type()

	byColumn := make(map[string]int, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Tag.Get("db")
		if name == "-" {
			continue
		}
		if name == "" {
			name = toSnakeCase(f.Name)
		}
		byColumn[name] = i
	}

	targets := make([]any, len(cols))
	for i, col := range cols {
		idx, ok := byColumn[col]
		if !ok {
			return nil, fmt.Errorf("no field tagged `db:%q` (or named %q) on %s", col, col, t)
		}
		targets[i] = elem.Field(idx).Addr().Interface()
	}
	return targets, nil
}

var snakeCaseBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])|([A-Z]+)([A-Z][a-z])`)

func toSnakeCase(s string) string {
	return strings.ToLower(snakeCaseBoundary.ReplaceAllString(s, "${1}${3}_${2}${4}"))
}

// AssertIndexExists fails the test unless an index named name exists in
// the tenant schema (for migration tests).
func (d *TestDB) AssertIndexExists(name string) {
	d.t.Helper()
	if !d.indexExists(name) {
		d.t.Fatalf("modeltest: expected index %s to exist", name)
	}
}

// AssertIndexNotExists fails the test if an index named name exists in
// the tenant schema.
func (d *TestDB) AssertIndexNotExists(name string) {
	d.t.Helper()
	if d.indexExists(name) {
		d.t.Fatalf("modeltest: expected index %s not to exist", name)
	}
}

func (d *TestDB) indexExists(name string) bool {
	d.t.Helper()
	var n int
	err := d.db.QueryRow(
		`SELECT count(*) FROM pg_indexes WHERE schemaname = $1 AND indexname = $2`,
		strings.Trim(d.schema(), `"`), name,
	).Scan(&n)
	if err != nil {
		d.t.Fatalf("modeltest: query pg_indexes: %v", err)
	}
	return n > 0
}

// AssertColumnExists fails the test unless table has a column named
// column in the tenant schema.
func (d *TestDB) AssertColumnExists(table, column string) {
	d.t.Helper()
	var n int
	err := d.db.QueryRow(
		`SELECT count(*) FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 AND column_name = $3`,
		strings.Trim(d.schema(), `"`), table, column,
	).Scan(&n)
	if err != nil {
		d.t.Fatalf("modeltest: query information_schema.columns: %v", err)
	}
	if n == 0 {
		d.t.Fatalf("modeltest: expected column %s.%s to exist", table, column)
	}
}
