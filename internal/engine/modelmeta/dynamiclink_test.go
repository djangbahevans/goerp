package modelmeta

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestValidateDynamicLink(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		other   model.NamedField
		wantErr string
	}{
		{name: "distinct expansion", field: "references_id"},
		{name: "missing suffix", field: "references", wantErr: "must end in _id"},
		{name: "empty stem", field: "_id", wantErr: "non-empty stem"},
		{
			name:    "stored field collision",
			field:   "references_id",
			other:   model.NamedField{Name: "references", Def: model.Text()},
			wantErr: `expanded read key "references" conflicts`,
		},
		{
			name:    "discriminator collision",
			field:   "reference_type_id",
			wantErr: `expanded read key "reference_type" conflicts`,
		},
		{
			name:    "relation expansion collision",
			field:   "references_id",
			other:   model.NamedField{Name: "reference_ids", Def: model.Many2Many("widgets.reference")},
			wantErr: `expanded read key "references" conflicts`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field := model.NamedField{Name: tt.field, Def: model.DynamicLink("reference_type")}
			md := *model.Define("widgets.comment").
				Field("reference_type", model.Selection("widgets.widget")).
				Field(field.Name, field.Def)
			if tt.other.Name != "" {
				md.Fields = append(md.Fields, tt.other)
			}

			err := ValidateDynamicLink(md, field)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("valid declaration: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
