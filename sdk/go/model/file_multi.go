package model

// FileMulti declares tenant file attachments in an engine-owned join table.
// The owning model needs a single UUID key and the field name ends in _ids.
// Writes replace the ID set; omission preserves links and an empty array clears
// them. Required demands at least one file. Reads expand active files under the
// name with _ids replaced by s. Removing links leaves the files intact.
func FileMulti(joinTable string) FieldDef {
	return FieldDef{Kind: KindFileMulti, JoinTable: joinTable}
}
