package def

import (
	"slices"
	"testing"
)

func TestValidateFieldType(t *testing.T) {
	types := []string{
		TypeString,
		TypeInteger,
		TypeFloat,
		TypeBoolean,
		TypeDuration,
		TypeJSON,
		TypeStringList,
		TypeIntegerList,
		TypeFloatList,
	}
	type fieldCase struct {
		field   string
		types   []string
		options bool
	}

	cases := []fieldCase{
		{
			field:   "text",
			types:   []string{TypeString, TypeDuration},
			options: false,
		},
		{
			field:   "number",
			types:   []string{TypeInteger, TypeFloat},
			options: false,
		},
		{
			field:   "integer",
			types:   []string{TypeInteger},
			options: false,
		},
		{
			field:   "boolean",
			types:   []string{TypeBoolean},
			options: false,
		},
		{
			field:   "toggle",
			types:   []string{TypeBoolean},
			options: false,
		},
		{
			field:   "duration",
			types:   []string{TypeDuration},
			options: false,
		},
		{
			field:   "json",
			types:   []string{TypeJSON},
			options: false,
		},
		{
			field:   "select",
			types:   []string{TypeString, TypeInteger, TypeFloat},
			options: true,
		},
		{
			field:   "radio",
			types:   []string{TypeString, TypeInteger, TypeFloat},
			options: true,
		},
		{
			field:   "multi_select",
			types:   []string{TypeStringList, TypeIntegerList, TypeFloatList},
			options: true,
		},
		{
			field:   "multiselect",
			types:   []string{TypeStringList, TypeIntegerList, TypeFloatList},
			options: true,
		},
	}
	for _, field := range []string{"email", "url", "phone", "textarea", "password", "country_select", "language_select", "timezone_select", "currency_select", "color_picker", "icon_picker"} {
		cases = append(cases, fieldCase{
			field: field,
			types: []string{TypeString},
		})
	}

	for _, test := range cases {
		for _, typ := range types {
			t.Run(test.field+"/"+typ, func(t *testing.T) {
				err := ValidateFieldType(typ, test.field, test.options)
				wantValid := slices.Contains(test.types, typ)

				if (err == nil) != wantValid {
					t.Fatalf("ValidateFieldType = %v, want valid %v", err, wantValid)
				}

				if test.options && ValidateFieldType(typ, test.field, false) == nil {
					t.Fatal("static selection accepted without options")
				}
			})
		}
	}

	for _, typ := range types {
		if err := ValidateFieldType(typ, "", false); err != nil {
			t.Errorf("omitted field type for %s: %v", typ, err)
		}
	}

	for _, field := range []string{"relation", "many2many", "tags", "address", "date_range", "computed_display", "missing"} {
		if err := ValidateFieldType(TypeString, field, true); err == nil {
			t.Errorf("unsupported widget %s accepted", field)
		}
	}
}
