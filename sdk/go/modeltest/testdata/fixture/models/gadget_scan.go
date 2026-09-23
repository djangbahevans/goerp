package models

import "github.com/djangbahevans/goerp/sdk/go/orm"

// Temporary scaffolding (goerp#974) — deleted once goerp#977 generates
// Scan directly into gadget.gen.go.

// Scan implements sdk/go/orm's reflection-free decode contract.
func (g *Gadget) Scan(row map[string]any) error {
	base, err := scanStandardFields("Gadget", row)
	if err != nil {
		return err
	}
	g.ID, g.TenantID, g.CreatedAt, g.UpdatedAt, g.DeletedAt, g.CreatedBy, g.Etag =
		base.ID, base.TenantID, base.CreatedAt, base.UpdatedAt, base.DeletedAt, base.CreatedBy, base.Etag

	if v, ok := row["name"]; ok {
		s, ok := v.(string)
		if !ok {
			return orm.NewDecodeError("Gadget", "Name", "string", v)
		}
		g.Name = s
	}
	if v, ok := row["display_name"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return orm.NewDecodeError("Gadget", "DisplayName", "*string", v)
		}
		g.DisplayName = &s
	}
	if v, ok := row["state"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return orm.NewDecodeError("Gadget", "State", "*GadgetState", v)
		}
		state := GadgetState(s)
		g.State = &state
	}
	return nil
}
