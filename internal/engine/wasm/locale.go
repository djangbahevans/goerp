package wasm

import (
	"context"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/l10n"
)

type invocationLocaleKey struct{}

type invocationLocale struct {
	tenantID string
	userID   string
	info     abiv1.LocaleInfo
}

type localeOverrideKey struct{}

func WithInvocationLocale(ctx context.Context, tenantID, userID string, info abiv1.LocaleInfo) context.Context {
	// Consumed overrides must not cross tenant or acting-user boundaries.
	ctx = context.WithValue(ctx, localeOverrideKey{}, "")

	return context.WithValue(ctx, invocationLocaleKey{}, invocationLocale{
		tenantID: tenantID,
		userID:   userID,
		info:     info,
	})
}

func WithLocaleOverride(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, localeOverrideKey{}, locale)
}

func (r *Runtime) SetLocaleResolver(resolve func(context.Context, string, string) (abiv1.LocaleInfo, error)) {
	r.localeResolver = resolve
}

func (r *Runtime) ResolveLocale(ctx context.Context, tenantID, userID string) (abiv1.LocaleInfo, error) {
	if snapshot, ok := ctx.Value(invocationLocaleKey{}).(invocationLocale); ok && snapshot.tenantID == tenantID && snapshot.userID == userID {
		return snapshot.info, nil
	}

	info := abiv1.LocaleInfo{Locale: l10n.PlatformDefaultLocale, Timezone: l10n.PlatformDefaultTimezone}
	if r != nil && r.localeResolver != nil {
		var err error
		info, err = r.localeResolver(ctx, tenantID, userID)
		if err != nil {
			return abiv1.LocaleInfo{}, err
		}
	}
	if locale, ok := ctx.Value(localeOverrideKey{}).(string); ok && locale != "" {
		info.Locale = locale
	}

	info.Direction = l10n.Direction(info.Locale)

	return info, nil
}

func (r *Runtime) PrepareInvocationLocale(ctx context.Context, mc *ModuleContext) (context.Context, error) {
	if !mc.localeResolved {
		info, err := r.ResolveLocale(ctx, mc.TenantID, mc.UserID)
		if err != nil {
			return ctx, err
		}
		mc.SetLocale(info)
	}

	return WithInvocationLocale(ctx, mc.TenantID, mc.UserID, mc.Locale), nil
}

func (mc *ModuleContext) SetLocale(info abiv1.LocaleInfo) {
	mc.Locale = info
	mc.localeResolved = true
}
