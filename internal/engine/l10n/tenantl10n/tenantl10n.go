// Package tenantl10n is a tenant's locale settings (l10n-guide.md §2
// "Tenant default locale", shell-ux.md §5.5): default locale and timezone,
// the locales its users may choose, first day of week and number format,
// stored in tenantconfig's l10n.* keys. Load resolves them against the
// platform defaults, so every reader (GET /auth/me, PATCH /auth/me's locale
// check, the tenant settings page) sees the same effective values.
package tenantl10n

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/l10n"
)

// Tenant config keys. available_locales is comma-separated.
const (
	KeyDefaultLocale    = "l10n.default_locale"
	KeyDefaultTimezone  = "l10n.default_timezone"
	KeyAvailableLocales = "l10n.available_locales"
	KeyFirstDayOfWeek   = "l10n.first_day_of_week"
	KeyNumberFormat     = "l10n.number_format"
)

// FirstDaysOfWeek and NumberFormats are the accepted values, each list's
// first entry the default. A number format is named by how it writes
// 1234.56.
var (
	FirstDaysOfWeek = []string{"monday", "sunday"}
	NumberFormats   = []string{"1,234.56", "1.234,56", "1 234,56"}
)

type Settings struct {
	DefaultLocale    string
	DefaultTimezone  string
	AvailableLocales []string
	FirstDayOfWeek   string
	NumberFormat     string
}

// Config is satisfied by tenantconfig.Store.
type Config interface {
	GetPrefix(ctx context.Context, tenantID, prefix string) (map[string]string, error)
}

type Store struct {
	config          Config
	platformLocales []string
}

// NewStore takes GOERP_AVAILABLE_LOCALES as platformLocales, the set a
// tenant's available locales are chosen from.
func NewStore(config Config, platformLocales []string) *Store {
	return &Store{config: config, platformLocales: platformLocales}
}

func (s *Store) PlatformLocales() []string {
	return s.platformLocales
}

// Load returns tenantID's effective settings. An unset or no-longer-valid
// value falls back to its default, so a locale the platform has since
// dropped is left out, and a default locale outside the available ones
// becomes the platform default, or else the first available locale.
func (s *Store) Load(ctx context.Context, tenantID string) (Settings, error) {
	values, err := s.config.GetPrefix(ctx, tenantID, "l10n.")
	if err != nil {
		return Settings{}, fmt.Errorf("load locale settings: %w", err)
	}

	var available []string
	if v, ok := values[KeyAvailableLocales]; ok {
		for locale := range strings.SplitSeq(v, ",") {
			if slices.Contains(s.platformLocales, locale) && !slices.Contains(available, locale) {
				available = append(available, locale)
			}
		}
	}
	if len(available) == 0 {
		available = slices.Clone(s.platformLocales)
	}

	settings := Settings{
		DefaultLocale:    l10n.PlatformDefaultLocale,
		DefaultTimezone:  l10n.PlatformDefaultTimezone,
		AvailableLocales: available,
		FirstDayOfWeek:   FirstDaysOfWeek[0],
		NumberFormat:     NumberFormats[0],
	}
	if v := values[KeyDefaultLocale]; slices.Contains(available, v) {
		settings.DefaultLocale = v
	} else if !slices.Contains(available, settings.DefaultLocale) && len(available) > 0 {
		settings.DefaultLocale = available[0]
	}
	if v := values[KeyDefaultTimezone]; l10n.ValidTimezone(v) {
		settings.DefaultTimezone = v
	}
	if v := values[KeyFirstDayOfWeek]; slices.Contains(FirstDaysOfWeek, v) {
		settings.FirstDayOfWeek = v
	}
	if v := values[KeyNumberFormat]; slices.Contains(NumberFormats, v) {
		settings.NumberFormat = v
	}
	return settings, nil
}
