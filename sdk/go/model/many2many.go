package model

// M2MOpt configures the join table of a Many2Many field.
type M2MOpt func(*FieldDef)

// Many2Many declares a set of related records stored in an engine-owned join
// table. The field name ends in _ids, and Through, M2MColumn1 and M2MColumn2
// are required. Both models need a single UUID primary key, and relatedModel
// belongs to the declaring module or one of its hard dependencies.
//
// Writes replace the set with an ID array; omission preserves links and an
// empty array clears them. Reads return the IDs under the field name and the
// expanded records under the name with _ids replaced by s (tag_ids → tags).
func Many2Many(relatedModel string, opts ...M2MOpt) FieldDef {
	f := FieldDef{Kind: KindMany2Many, RelatedModel: relatedModel}
	for _, opt := range opts {
		opt(&f)
	}
	return f
}

// Through names the engine-owned join table in the tenant schema.
func Through(table string) M2MOpt {
	return func(f *FieldDef) { f.JoinTable = table }
}

// M2MColumn1 names the join column referencing the declaring record's UUID key.
func M2MColumn1(column string) M2MOpt {
	return func(f *FieldDef) { f.Column1 = column }
}

// M2MColumn2 names the join column referencing the related record's UUID key.
func M2MColumn2(column string) M2MOpt {
	return func(f *FieldDef) { f.Column2 = column }
}

// RetainArchived keeps readable archived records in a Many2Many field's IDs and
// expansions. Updates may retain existing archived links, but cannot add them.
// Picker and filter queries continue to exclude archived records.
func RetainArchived() M2MOpt {
	return func(f *FieldDef) { f.RetainArchived = true }
}
