package notify

import (
	"context"
	"maps"
	"slices"

	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
)

// sendTemplates are the notification_templates rows one type's send
// renders from for one recipient locale: per channel, the row (override,
// else default) of each of the locale's fallback locales that has one,
// closest first.
type sendTemplates map[string][]notiftemplate.Row

func loadSendTemplates(ctx context.Context, store *notifications.Store, tenantSlug, templateKey, userLocale string) (sendTemplates, error) {
	templates, err := loadSendTemplatesForLocales(ctx, store, tenantSlug, templateKey, map[string]bool{userLocale: true})
	if err != nil {
		return nil, err
	}

	return templates[userLocale], nil
}

func loadSendTemplatesForLocales(ctx context.Context, store *notifications.Store, tenantSlug, templateKey string, userLocales map[string]bool) (map[string]sendTemplates, error) {
	candidates := make(map[string]bool)
	for locale := range userLocales {
		for _, candidate := range notiftemplate.LocaleCandidates(locale) {
			candidates[candidate] = true
		}
	}

	rows, err := store.Templates(ctx, tenantSlug, templateKey, slices.Collect(maps.Keys(candidates)))
	if err != nil {
		return nil, err
	}

	byLocale := make(map[string][]notiftemplate.Row)
	for _, row := range rows {
		byLocale[row.Locale] = append(byLocale[row.Locale], row.Row)
	}

	templates := make(map[string]sendTemplates, len(userLocales))
	for locale := range userLocales {
		st := sendTemplates{}
		for _, candidate := range notiftemplate.LocaleCandidates(locale) {
			for _, row := range byLocale[candidate] {
				st[row.Channel] = append(st[row.Channel], row)
			}
		}
		templates[locale] = st
	}

	return templates, nil
}

// part returns the columns cols of channel's template from the closest
// locale whose row defines any of them. An email's subject, HTML body and
// plain-text body are each a part of their own, so each falls back
// through the locales independently, as their separate package files do.
func (st sendTemplates) part(channel string, cols ...string) (map[string]string, bool) {
	for _, r := range st[channel] {
		if slices.ContainsFunc(cols, func(c string) bool { _, ok := r.Fields[c]; return ok }) {
			return r.Fields, true
		}
	}
	return nil, false
}

// renderColumn renders column col of fields against vars; a column fields
// leaves out renders as "".
func renderColumn(fields map[string]string, col string, vars map[string]any) (string, error) {
	src, ok := fields[col]
	if !ok {
		return "", nil
	}
	return notiftemplate.RenderColumn(col, src, vars)
}
