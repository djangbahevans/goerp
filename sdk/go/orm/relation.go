package orm

// RelationRef is a Many2One field's expanded read-time object —
// go-sdk-reference.md §22 "Many2One": {id, display_name}.
type RelationRef struct {
	ID          string `db:"id"`
	DisplayName string `db:"display_name"`
}
