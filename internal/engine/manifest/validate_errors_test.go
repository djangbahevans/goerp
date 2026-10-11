package manifest

import (
	"strings"
	"testing"
)

func TestValidateErrorCodes(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"contacts.contact.not_found", ""},
		{"contacts.resource_2.custom_3", ""},
		{"sales.contact.not_found", "belongs to module"},
		{"engine.route.not_found", "belongs to module"},
		{"contacts.contact", "module.resource.condition"},
		{"contacts.contact.condition.extra", "module.resource.condition"},
		{"contacts.Contact.not_found", "module.resource.condition"},
		{"contacts.2contact.not_found", "module.resource.condition"},
		{"contacts.contact._condition", "module.resource.condition"},
		{"contacts.contact.not-found", "module.resource.condition"},
		{"contacts..not_found", "module.resource.condition"},
		{"", "module.resource.condition"},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			err := validateErrorCodes(Manifest{Name: "contacts", ErrorCodes: []string{tt.code}})
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "error_codes[0]") {
				t.Fatalf("error = %v, want indexed diagnostic containing %q", err, tt.want)
			}
		})
	}
}
