package manifest

import (
	"strings"
	"testing"
)

func TestWebhookContentTypeIsConnectorOnly(t *testing.T) {
	for _, moduleType := range []string{"domain", "l10n", "connector", "bridge", "theme", "report_bundle", "automation", "field_extension"} {
		t.Run(moduleType, func(t *testing.T) {
			err := validateModuleType(Manifest{Type: moduleType, Wasm: true, WebhookContentType: "application/json"})
			if moduleType == "connector" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "must not declare webhook_content_type") {
				t.Fatalf("type %s: error = %v, want a connector-only rejection", moduleType, err)
			}
		})
	}
}

func TestWebhookContentTypeMustBeABareMediaType(t *testing.T) {
	tests := []struct {
		value   string
		wantErr bool
	}{
		{"application/json", false},
		{"application/x-www-form-urlencoded", false},
		{"application/vnd.api+json", false},
		{"application/json; charset=utf-8", true},
		{"Application/JSON", true},
		{"json", true},
		{"application/", true},
		{"/json", true},
		{"application/json application/xml", true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			err := validateModuleType(Manifest{Type: "connector", Wasm: true, WebhookContentType: tt.value})
			if (err != nil) != tt.wantErr {
				t.Errorf("validateModuleType() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWebhookMediaType_DefaultsToJSON(t *testing.T) {
	if got := (Manifest{}).WebhookMediaType(); got != "application/json" {
		t.Errorf("default = %q, want application/json", got)
	}
	if got := (Manifest{WebhookContentType: "application/x-www-form-urlencoded"}).WebhookMediaType(); got != "application/x-www-form-urlencoded" {
		t.Errorf("declared = %q, want the declared type", got)
	}
}
