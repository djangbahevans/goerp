package notify

import (
	"cmp"
	"context"
	"slices"

	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
)

// sendTemplates are the notification_templates rows one type's send
// renders from for one recipient locale: per channel, the row (override,
// else default) of each of the locale's fallback locales that has one,
// closest first.
type sendTemplates map[string][]notiftemplate.Row

// loadSendTemplates loads templateKey's rows for userLocale's fallback
// locales from tenantSlug's notification_templates.
func loadSendTemplates(ctx context.Context, store *notifications.Store, tenantSlug, templateKey, userLocale string) (sendTemplates, error) {
	candidates := notiftemplate.LocaleCandidates(userLocale)
	rows, err := store.Templates(ctx, tenantSlug, templateKey, candidates)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b notifications.StoredTemplate) int {
		return cmp.Compare(slices.Index(candidates, a.Locale), slices.Index(candidates, b.Locale))
	})
	st := sendTemplates{}
	for _, r := range rows {
		st[r.Channel] = append(st[r.Channel], r.Row)
	}
	return st, nil
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
