package manifest

import (
	"strings"
	"testing"
)

func TestHTTPFetchCapabilityIsConnectorOnly(t *testing.T) {
	for _, moduleType := range []string{"domain", "l10n", "connector", "bridge", "theme", "report_bundle", "automation", "field_extension"} {
		t.Run(moduleType, func(t *testing.T) {
			err := validateModuleType(Manifest{Type: moduleType, Wasm: true, Capabilities: []string{"http.fetch"}})
			if moduleType == "connector" {
				if err != nil {
					t.Fatal(err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), "must not declare http.fetch") {
				t.Fatalf("type %s: error = %v, want connector-only capability rejection", moduleType, err)
			}
		})
	}
}
