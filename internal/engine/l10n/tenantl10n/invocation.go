package tenantl10n

import (
	"context"
	"errors"
	"fmt"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/l10n"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

type UserProfiles interface {
	GetProfile(context.Context, string) (*user.Profile, error)
}

type TenantProfiles interface {
	GetProfile(context.Context, string) (*tenant.Profile, error)
}

type InvocationResolver struct {
	Settings *Store
	Users    UserProfiles
	Tenants  TenantProfiles
}

func (r *InvocationResolver) Resolve(ctx context.Context, tenantID, userID string) (abiv1.LocaleInfo, error) {
	info := abiv1.LocaleInfo{Locale: l10n.PlatformDefaultLocale, Timezone: l10n.PlatformDefaultTimezone}
	if tenantID != "" {
		settings, err := r.Settings.Load(ctx, tenantID)
		if err != nil {
			return info, err
		}
		info.Locale, info.Timezone = settings.DefaultLocale, settings.DefaultTimezone

		profile, err := r.Tenants.GetProfile(ctx, tenantID)
		if err != nil {
			return info, fmt.Errorf("load tenant currency: %w", err)
		}
		if profile.DefaultCurrency != nil {
			info.Currency = *profile.DefaultCurrency
		}
	}

	if userID != "" {
		profile, err := r.Users.GetProfile(ctx, userID)
		if err != nil && !errors.Is(err, user.ErrProfileNotFound) {
			return info, fmt.Errorf("load user locale: %w", err)
		}
		if err == nil {
			if profile.Locale != nil && *profile.Locale != "" {
				info.Locale = *profile.Locale
			}
			if profile.Timezone != nil && *profile.Timezone != "" {
				info.Timezone = *profile.Timezone
			}
		}
	}

	info.Direction = l10n.Direction(info.Locale)

	return info, nil
}
