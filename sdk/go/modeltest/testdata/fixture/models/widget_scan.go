package models

// Temporary scaffolding (goerp#974) — deleted once goerp#977 generates
// Scan directly into widget.gen.go.

// Scan implements sdk/go/orm's reflection-free decode contract.
func (w *Widget) Scan(row map[string]any) error {
	base, err := scanStandardFields("Widget", row)
	if err != nil {
		return err
	}
	w.ID, w.TenantID, w.CreatedAt, w.UpdatedAt, w.DeletedAt, w.CreatedBy, w.Etag =
		base.ID, base.TenantID, base.CreatedAt, base.UpdatedAt, base.DeletedAt, base.CreatedBy, base.Etag
	return nil
}
