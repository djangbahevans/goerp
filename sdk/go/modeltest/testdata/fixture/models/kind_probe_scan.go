package models

import (
	"time"

	"github.com/djangbahevans/goerp/sdk/go/orm"
)

// Temporary scaffolding (goerp#974) — deleted once goerp#977 generates
// Scan directly into kind_probe.gen.go.

// Scan implements sdk/go/orm's reflection-free decode contract. Field Go
// types here match goerp#960's own empirical findings (harness_test.go's
// TestKindProbe_FieldKindsRoundTrip): Decimal and Time decode as string,
// TimestampTZ and Date as time.Time, JSONB/Bytea as []byte, Enum as its
// declared value decoded as a plain string, and a Many2One expansion as
// a nested map[string]any ({id, display_name} — host_orm_relations.go).
func (k *KindProbe) Scan(row map[string]any) error {
	base, err := scanStandardFields("KindProbe", row)
	if err != nil {
		return err
	}
	k.ID, k.TenantID, k.CreatedAt, k.UpdatedAt, k.DeletedAt, k.CreatedBy, k.Etag =
		base.ID, base.TenantID, base.CreatedAt, base.UpdatedAt, base.DeletedAt, base.CreatedBy, base.Etag

	if v, ok := row["decimal_field"]; ok {
		s, ok := v.(string)
		if !ok {
			return orm.NewDecodeError("KindProbe", "DecimalField", "string", v)
		}
		k.DecimalField = s
	}
	if v, ok := row["timestamp_field"]; ok {
		t, ok := v.(time.Time)
		if !ok {
			return orm.NewDecodeError("KindProbe", "TimestampField", "time.Time", v)
		}
		k.TimestampField = t
	}
	if v, ok := row["date_field"]; ok {
		t, ok := v.(time.Time)
		if !ok {
			return orm.NewDecodeError("KindProbe", "DateField", "time.Time", v)
		}
		k.DateField = t
	}
	if v, ok := row["time_field"]; ok {
		s, ok := v.(string)
		if !ok {
			return orm.NewDecodeError("KindProbe", "TimeField", "string", v)
		}
		k.TimeField = s
	}
	if v, ok := row["jsonb_field"]; ok {
		b, ok := v.([]byte)
		if !ok {
			return orm.NewDecodeError("KindProbe", "JsonbField", "[]byte", v)
		}
		k.JsonbField = b
	}
	if v, ok := row["bytea_field"]; ok {
		b, ok := v.([]byte)
		if !ok {
			return orm.NewDecodeError("KindProbe", "ByteaField", "[]byte", v)
		}
		k.ByteaField = b
	}
	if v, ok := row["optional_note"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return orm.NewDecodeError("KindProbe", "OptionalNote", "*string", v)
		}
		k.OptionalNote = &s
	}
	if v, ok := row["created_by_gadget_id"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return orm.NewDecodeError("KindProbe", "CreatedByGadgetID", "*string", v)
		}
		k.CreatedByGadgetID = &s
	}
	if v, ok := row["created_by_gadget"]; ok && v != nil {
		nested, ok := v.(map[string]any)
		if !ok {
			return orm.NewDecodeError("KindProbe", "CreatedByGadget", "*orm.RelationRef", v)
		}
		ref := orm.RelationRef{}
		if id, ok := nested["id"].(string); ok {
			ref.ID = id
		}
		if dn, ok := nested["display_name"].(string); ok {
			ref.DisplayName = dn
		}
		k.CreatedByGadget = &ref
	}
	if v, ok := row["priority"]; ok {
		s, ok := v.(string)
		if !ok {
			return orm.NewDecodeError("KindProbe", "Priority", "KindProbePriority", v)
		}
		k.Priority = KindProbePriority(s)
	}
	return nil
}
