package loader

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
)

func loadedModule(mf manifest.Manifest) *module.LoadedModule {
	return &module.LoadedModule{Manifest: mf, Status: module.StatusReady}
}

func TestValidateEventSubscriptions_ExactVersionMustBeEmitted(t *testing.T) {
	emitter := loadedModule(manifest.Manifest{
		Name:  "sales",
		Emits: []manifest.EventDeclaration{{Name: "sales.order.confirmed", Version: 1}, {Name: "sales.order.confirmed", Version: 2}},
	})

	tests := []struct {
		name    string
		sub     manifest.EventSubscription
		soft    []string
		wantErr string
	}{
		{"emitted v2", manifest.EventSubscription{Name: "sales.order.confirmed", Version: 2}, nil, ""},
		{"omitted version means v1", manifest.EventSubscription{Name: "sales.order.confirmed"}, nil, ""},
		{"unemitted v3", manifest.EventSubscription{Name: "sales.order.confirmed", Version: 3}, nil, `version 3, which no loaded module emits`},
		{"unknown event", manifest.EventSubscription{Name: "sales.order.missing"}, nil, `unknown event "sales.order.missing"`},
		{"unemitted version of a soft dependency's event", manifest.EventSubscription{Name: "sales.order.confirmed", Version: 3}, []string{"sales"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subscriber := loadedModule(manifest.Manifest{
				Name: "inventory", Subscribes: []manifest.EventSubscription{tc.sub}, SoftDependsOn: tc.soft,
			})

			ValidateEventSubscriptions(map[string]*module.LoadedModule{"sales": emitter, "inventory": subscriber})

			switch {
			case tc.wantErr == "" && subscriber.Status == module.StatusFailed:
				t.Errorf("subscriber failed: %s", subscriber.FailureReason)
			case tc.wantErr != "" && (subscriber.Status != module.StatusFailed || !strings.Contains(subscriber.FailureReason, tc.wantErr)):
				t.Errorf("status = %v, reason = %q, want a failure containing %q", subscriber.Status, subscriber.FailureReason, tc.wantErr)
			}
		})
	}
}
