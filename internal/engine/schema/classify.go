package schema

import (
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

func (e *SchemaDiffEngine) classifyChanges(changes []schema.Change) (safe, blocked []tableChange) {
	for _, tc := range explodeChanges(changes) {
		switch c := tc.change.(type) {
		case *schema.AddTable, *schema.AddIndex, *schema.DropIndex:
			safe = append(safe, tc)
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
