package models

// Temporary scaffolding (goerp#974) — deleted once goerp#977 generates
// Scan directly into gizmo.gen.go.

// Scan implements sdk/go/orm's reflection-free decode contract.
func (g *Gizmo) Scan(row map[string]any) error {
	base, err := scanStandardFields("Gizmo", row)
	if err != nil {
		return err
	}
	g.ID, g.TenantID, g.CreatedAt, g.UpdatedAt, g.DeletedAt, g.CreatedBy, g.Etag =
		base.ID, base.TenantID, base.CreatedAt, base.UpdatedAt, base.DeletedAt, base.CreatedBy, base.Etag
	return nil
}
