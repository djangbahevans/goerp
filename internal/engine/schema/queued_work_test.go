package schema

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestIsQueuedWorkCompatible(t *testing.T) {
	tests := []struct {
		name       string
		from       string
		to         string
		boundaries []string
		want       bool
		wantErr    bool
	}{
		{name: "untagged skips invalid migrations", to: "2.0.0", boundaries: []string{"invalid"}, want: true},
		{name: "same version skips invalid migrations", from: "2.0.0", to: "2.0.0", boundaries: []string{"invalid"}, want: true},
		{name: "additive upgrade", from: "1.0.0", to: "2.0.0", want: true},
		{name: "boundary equals pin", from: "1.0.0", to: "2.0.0", boundaries: []string{">= 1.0.0"}, want: true},
		{name: "boundary before pin", from: "1.0.0", to: "2.0.0", boundaries: []string{">= 0.9.0"}, want: true},
		{name: "boundary between versions", from: "1.0.0", to: "2.0.0", boundaries: []string{">= 1.5.0"}},
		{name: "boundary equals live version", from: "1.0.0", to: "2.0.0", boundaries: []string{">= 2.0.0"}},
		{name: "boundary after live version", from: "1.0.0", to: "2.0.0", boundaries: []string{">= 2.1.0"}, want: true},
		{name: "semantic version ordering", from: "1.9.0", to: "1.11.0", boundaries: []string{">= 1.10.0"}},
		{name: "prerelease boundary crossed", from: "1.0.0", to: "2.0.0-rc.2", boundaries: []string{">= 2.0.0-rc.1"}},
		{name: "prerelease boundary not reached", from: "1.0.0", to: "2.0.0-rc.1", boundaries: []string{">= 2.0.0-rc.2"}, want: true},
		{name: "multiple boundaries", from: "1.0.0", to: "2.0.0", boundaries: []string{">= 3.0.0", ">= 0.9.0", ">= 1.5.0"}},
		{name: "downgrade has no forward crossing", from: "2.0.0", to: "1.0.0", boundaries: []string{">= 1.5.0"}, want: true},
		{name: "invalid pin", from: "invalid", to: "2.0.0", wantErr: true},
		{name: "invalid live version", from: "1.0.0", to: "invalid", wantErr: true},
		{name: "invalid boundary", from: "1.0.0", to: "2.0.0", boundaries: []string{"invalid"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var migrations []model.DataMigration
			for _, boundary := range tt.boundaries {
				migrations = append(migrations, model.DataMigration{ToVersion: boundary, Handler: "migrate"})
			}

			got, err := IsQueuedWorkCompatible(migrations, tt.from, tt.to)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("IsQueuedWorkCompatible() = %v, %v; want %v, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
