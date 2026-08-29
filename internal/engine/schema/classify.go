package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
)

type tableChange struct {
	table  *schema.Table
	change schema.Change
}

func explodeChanges(changes []schema.Change) []tableChange {
	var out []tableChange
	for _, c := range changes {
		if mt, ok := c.(*schema.ModifyTable); ok {
			for _, inner := range mt.Changes {
				out = append(out, tableChange{table: mt.T, change: inner})
			}
			continue
		}
		out = append(out, tableChange{change: c})
	}
	return out
}

// classifyChanges sorts changes into three buckets: safe (applied inline as
// declared), deferred (applied inline but as NOT VALID, then validated in
// the background — see apply.go's Execute and schema.ValidateConstraintWorker),
// and blocked (skipped entirely; requires an explicit data migration
// handler, which does not exist yet).
func (e *SchemaDiffEngine) classifyChanges(changes []schema.Change) (safe, deferred, blocked []tableChange) {
	for _, tc := range explodeChanges(changes) {
		switch c := tc.change.(type) {
		case *schema.AddTable, *schema.AddIndex, *schema.DropIndex:
			safe = append(safe, tc)
		case *schema.AddCheck, *schema.AddForeignKey:
			deferred = append(deferred, tc)
		case *schema.AddColumn:
			if isSafeAddColumn(c) {
				safe = append(safe, tc)
			} else {
				blocked = append(blocked, tc)
			}
		case *schema.ModifyColumn:
			if isSafeModifyColumn(c) {
				safe = append(safe, tc)
			} else {
				blocked = append(blocked, tc)
			}
		case *schema.DropColumn, *schema.DropTable, *schema.RenameColumn, *schema.RenameTable:
			blocked = append(blocked, tc)
		default:
			safe = append(safe, tc)
		}
	}
	return
}

func isSafeAddColumn(c *schema.AddColumn) bool {
	return c.C.Type.Null || c.C.Default != nil
}

func isSafeModifyColumn(c *schema.ModifyColumn) bool {
	if c.Change.Is(schema.ChangeGenerated) {
		return false
	}
	if c.Change.Is(schema.ChangeNull) && c.From.Type.Null && !c.To.Type.Null {
		return false
	}
	if c.Change.Is(schema.ChangeType) && !isWideningTypeChange(c.From.Type, c.To.Type) {
		return false
	}
	return true
}

// isWideningTypeChange reports whether the declared type change can only
// ever accept a superset of what the old type accepted — the one class of
// type change that can't reject or truncate an existing value. Any type
// pairing not explicitly recognized here is treated as unsafe: narrowing
// and cross-kind changes are far more common failure modes than missed
// widenings, so the default has to be "block", not "allow".
func isWideningTypeChange(from, to *schema.ColumnType) bool {
	switch f := from.Type.(type) {
	case *schema.StringType:
		t, ok := to.Type.(*schema.StringType)
		if !ok {
			return false
		}
		if t.T == postgres.TypeText {
			return true // any bounded string widened to unbounded TEXT
		}
		return t.T == f.T && t.Size >= f.Size
	case *schema.IntegerType:
		t, ok := to.Type.(*schema.IntegerType)
		if !ok {
			return false
		}
		fr, tr := integerRank(f.T), integerRank(t.T)
		return fr >= 0 && tr >= fr
	case *schema.DecimalType:
		t, ok := to.Type.(*schema.DecimalType)
		if !ok {
			return false
		}
		return t.Scale == f.Scale && t.Precision >= f.Precision
	default:
		return false
	}
}

func integerRank(t string) int {
	switch t {
	case postgres.TypeSmallInt, postgres.TypeInt2:
		return 0
	case postgres.TypeInteger, postgres.TypeInt, postgres.TypeInt4:
		return 1
	case postgres.TypeBigInt, postgres.TypeInt8:
		return 2
	default:
		return -1
	}
}

// ChangeSummary is one tableChange's exported, JSON-ready shape — what
// GET /admin/modules/{name}/schema (goerp#292) reports for each pending
// change, and what POST /admin/schema/accept records Hash values from.
type ChangeSummary struct {
	Kind   string `json:"kind"`
	Table  string `json:"table"`
	Detail string `json:"detail,omitempty"`
	// Hash is only set for a blocked change — the identifier
	// RecordAcceptance stores and ExecuteAccepted later matches against.
	// A safe/deferred change never needs accepting, so it carries no hash.
	Hash string `json:"hash,omitempty"`
}

// describeChange extracts a change's identifying facts by hand rather
// than formatting the underlying ariga.io/atlas/sql/schema.Change value
// directly — those types hold pointers (e.g. a *Column back-referencing
// its *Table) whose default %v/%+v formatting isn't guaranteed stable
// across two separate Diff calls, which changeHash below needs to be.
func describeChange(tc tableChange) (kind, table, detail string) {
	if tc.table != nil {
		table = tc.table.Name
	}
	switch v := tc.change.(type) {
	case *schema.AddTable:
		return "add_table", v.T.Name, ""
	case *schema.DropTable:
		return "drop_table", v.T.Name, ""
	case *schema.RenameTable:
		return "rename_table", v.From.Name, fmt.Sprintf("-> %s", v.To.Name)
	case *schema.AddIndex:
		return "add_index", table, v.I.Name
	case *schema.DropIndex:
		return "drop_index", table, v.I.Name
	case *schema.AddCheck:
		return "add_check", table, v.C.Name
	case *schema.AddForeignKey:
		return "add_foreign_key", table, v.F.Symbol
	case *schema.AddColumn:
		return "add_column", table, fmt.Sprintf("%s %s", v.C.Name, v.C.Type.Raw)
	case *schema.ModifyColumn:
		return "modify_column", table, fmt.Sprintf("%s: %s -> %s", v.To.Name, v.From.Type.Raw, v.To.Type.Raw)
	case *schema.DropColumn:
		return "drop_column", table, v.C.Name
	case *schema.RenameColumn:
		return "rename_column", table, fmt.Sprintf("%s -> %s", v.From.Name, v.To.Name)
	default:
		return fmt.Sprintf("%T", tc.change), table, ""
	}
}

// changeHash returns a stable identifier for one blocked change, built
// from describeChange's hand-extracted fields (see that function's own
// doc comment for why). POST /admin/schema/accept computes this for
// every currently-blocked change and records it in
// system.schema_sync_acceptances; ExecuteAccepted recomputes the same
// hash for whatever Diff proposes at resync time and applies any blocked
// change that matches.
func changeHash(tc tableChange) string {
	kind, table, detail := describeChange(tc)
	sum := sha256.Sum256([]byte(kind + "|" + table + "|" + detail))
	return hex.EncodeToString(sum[:])
}

func summarizeChanges(tcs []tableChange, includeHash bool) []ChangeSummary {
	out := make([]ChangeSummary, len(tcs))
	for i, tc := range tcs {
		kind, table, detail := describeChange(tc)
		s := ChangeSummary{Kind: kind, Table: table, Detail: detail}
		if includeHash {
			s.Hash = changeHash(tc)
		}
		out[i] = s
	}
	return out
}

// Classify splits changes into the same safe/deferred/blocked buckets
// Execute applies internally, as JSON-ready summaries — goerp#292's
// GET /admin/modules/{name}/schema calls this (after Diff, never
// Execute) to report what a sync would do without doing it.
func (e *SchemaDiffEngine) Classify(changes []schema.Change) (safe, deferred, blocked []ChangeSummary) {
	s, d, b := e.classifyChanges(changes)
	return summarizeChanges(s, false), summarizeChanges(d, false), summarizeChanges(b, true)
}
