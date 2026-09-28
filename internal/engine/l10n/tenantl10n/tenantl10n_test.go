package tenantl10n

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeConfig map[string]string

func (f fakeConfig) GetPrefix(context.Context, string, string) (map[string]string, error) {
	return f, nil
}

type failingConfig struct{}

func (failingConfig) GetPrefix(context.Context, string, string) (map[string]string, error) {
	return nil, errors.New("database down")
}

var platform = []string{"en", "fr", "ar"}

func TestLoad(t *testing.T) {
	cases := []struct {
		name   string
		stored fakeConfig
		want   Settings
	}{
		{
			name:   "nothing stored",
			stored: fakeConfig{},
			want:   Settings{DefaultLocale: "en", DefaultTimezone: "UTC", AvailableLocales: platform, FirstDayOfWeek: "monday", NumberFormat: "1,234.56"},
		},
		{
			name: "every value stored",
			stored: fakeConfig{
				KeyAvailableLocales: "fr,en", KeyDefaultLocale: "fr", KeyDefaultTimezone: "Africa/Accra",
				KeyFirstDayOfWeek: "sunday", KeyNumberFormat: "1 234,56",
			},
			want: Settings{DefaultLocale: "fr", DefaultTimezone: "Africa/Accra", AvailableLocales: []string{"fr", "en"}, FirstDayOfWeek: "sunday", NumberFormat: "1 234,56"},
		},
		{
			name:   "locales the platform dropped are left out",
			stored: fakeConfig{KeyAvailableLocales: "de,fr,fr"},
			want:   Settings{DefaultLocale: "fr", DefaultTimezone: "UTC", AvailableLocales: []string{"fr"}, FirstDayOfWeek: "monday", NumberFormat: "1,234.56"},
		},
		{
			name:   "no stored locale still offered falls back to the platform list",
			stored: fakeConfig{KeyAvailableLocales: "de"},
			want:   Settings{DefaultLocale: "en", DefaultTimezone: "UTC", AvailableLocales: platform, FirstDayOfWeek: "monday", NumberFormat: "1,234.56"},
		},
		{
			name:   "invalid values fall back to defaults",
			stored: fakeConfig{KeyDefaultLocale: "de", KeyDefaultTimezone: "Local", KeyFirstDayOfWeek: "friday", KeyNumberFormat: "1234.56"},
			want:   Settings{DefaultLocale: "en", DefaultTimezone: "UTC", AvailableLocales: platform, FirstDayOfWeek: "monday", NumberFormat: "1,234.56"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NewStore(c.stored, platform).Load(t.Context(), "tenant")
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Load() = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestLoad_ConfigErrorIsReturned(t *testing.T) {
	if _, err := NewStore(failingConfig{}, platform).Load(t.Context(), "tenant"); err == nil {
		t.Fatal("Load() error = nil, want the config read failure")
	}
}
