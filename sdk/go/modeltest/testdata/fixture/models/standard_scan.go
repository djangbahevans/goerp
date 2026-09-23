package models

// Temporary scaffolding (goerp#974) — deleted once goerp#977 generates
// Scan directly into each struct's own *.gen.go file. Every model here
// embeds WithStandardFields()'s own seven columns; scanStandardFields is
// their one shared decode block, so each sibling *_scan.go file below
// only has to hand-write its own model-specific fields.

import (
	"time"

	"github.com/djangbahevans/goerp/sdk/go/orm"
)

type standardFields struct {
	ID        string
	TenantID  string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
	CreatedBy *string
	Etag      string
}

func scanStandardFields(structName string, row map[string]any) (standardFields, error) {
	var f standardFields

	if v, ok := row["id"]; ok {
		s, ok := v.(string)
		if !ok {
			return f, orm.NewDecodeError(structName, "ID", "string", v)
		}
		f.ID = s
	}
	if v, ok := row["tenant_id"]; ok {
		s, ok := v.(string)
		if !ok {
			return f, orm.NewDecodeError(structName, "TenantID", "string", v)
		}
		f.TenantID = s
	}
	if v, ok := row["created_at"]; ok {
		t, ok := v.(time.Time)
		if !ok {
			return f, orm.NewDecodeError(structName, "CreatedAt", "time.Time", v)
		}
		f.CreatedAt = t
	}
	if v, ok := row["updated_at"]; ok {
		t, ok := v.(time.Time)
		if !ok {
			return f, orm.NewDecodeError(structName, "UpdatedAt", "time.Time", v)
		}
		f.UpdatedAt = t
	}
	if v, ok := row["deleted_at"]; ok && v != nil {
		t, ok := v.(time.Time)
		if !ok {
			return f, orm.NewDecodeError(structName, "DeletedAt", "*time.Time", v)
		}
		f.DeletedAt = &t
	}
	if v, ok := row["created_by"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return f, orm.NewDecodeError(structName, "CreatedBy", "*string", v)
		}
		f.CreatedBy = &s
	}
	if v, ok := row["etag"]; ok {
		s, ok := v.(string)
		if !ok {
			return f, orm.NewDecodeError(structName, "Etag", "string", v)
		}
		f.Etag = s
	}

	return f, nil
}
