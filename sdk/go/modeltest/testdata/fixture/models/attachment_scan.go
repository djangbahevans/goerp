package models

import "github.com/djangbahevans/goerp/sdk/go/orm"

// Temporary scaffolding (goerp#974) — deleted once goerp#977 generates
// Scan directly into attachment.gen.go.

// Scan implements sdk/go/orm's reflection-free decode contract.
func (a *Attachment) Scan(row map[string]any) error {
	base, err := scanStandardFields("Attachment", row)
	if err != nil {
		return err
	}
	a.ID, a.TenantID, a.CreatedAt, a.UpdatedAt, a.DeletedAt, a.CreatedBy, a.Etag =
		base.ID, base.TenantID, base.CreatedAt, base.UpdatedAt, base.DeletedAt, base.CreatedBy, base.Etag

	if v, ok := row["reference_type"]; ok {
		s, ok := v.(string)
		if !ok {
			return orm.NewDecodeError("Attachment", "ReferenceType", "string", v)
		}
		a.ReferenceType = s
	}
	if v, ok := row["reference_id"]; ok {
		s, ok := v.(string)
		if !ok {
			return orm.NewDecodeError("Attachment", "ReferenceID", "string", v)
		}
		a.ReferenceID = s
	}
	return nil
}
