package notify

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
)

// inAppContent is a rendered in_app template (notification-system.md §5
// "In-app template"): the notifications row's display fields.
type inAppContent struct {
	Title     string
	Body      string
	ActionURL string
	Icon      string
}

var inAppColumns = []string{notiftemplate.ColTitle, notiftemplate.ColBody, notiftemplate.ColActionURL, notiftemplate.ColIcon}

// renderInApp renders tmpls' in_app template. A type with no in_app
// template gets its label as title and nothing else.
func renderInApp(tmpls sendTemplates, label string, vars map[string]any) (inAppContent, error) {
	fields, ok := tmpls.part(notifications.ChannelInApp, inAppColumns...)
	if !ok {
		return inAppContent{Title: label}, nil
	}

	rendered := make([]string, len(inAppColumns))
	for i, col := range inAppColumns {
		var err error
		if rendered[i], err = renderColumn(fields, col, vars); err != nil {
			return inAppContent{}, fmt.Errorf("%w: in_app: %w", ErrRenderFailed, err)
		}
	}
	return inAppContent{Title: cmp.Or(rendered[0], label), Body: rendered[1], ActionURL: rendered[2], Icon: rendered[3]}, nil
}

// providerContent is the rendered text of a send's sms and push
// deliveries, empty for a channel the send does not go to. A channel whose
// template failed to render has its error instead: only the in_app
// template failing fails the send, so that channel's deliveries fail
// alone, as an email delivery's would.
type providerContent struct {
	smsBody   string
	pushTitle string
	pushBody  string
	failed    map[string]error
}

// renderProviderChannels renders the sms and push templates of the
// channels in plan (notification-system.md §5). A channel whose type has
// no template of its own sends the in_app title and body instead.
func renderProviderChannels(tmpls sendTemplates, plan []channelPlan, inApp inAppContent, vars map[string]any) providerContent {
	c := providerContent{failed: map[string]error{}}

	for _, cp := range plan {
		switch cp.channel {
		case notifications.ChannelSMS:
			c.smsBody = strings.Join(nonBlank(inApp.Title, inApp.Body), "\n")
			if fields, ok := tmpls.part(notifications.ChannelSMS, notiftemplate.ColSMS); ok {
				rendered, err := renderColumn(fields, notiftemplate.ColSMS, vars)
				if err != nil {
					c.failed[cp.channel] = fmt.Errorf("%w: sms: %w", ErrRenderFailed, err)
					continue
				}
				c.smsBody = strings.TrimSpace(rendered)
			}

		case notifications.ChannelPush:
			c.pushTitle, c.pushBody = inApp.Title, inApp.Body
			if fields, ok := tmpls.part(notifications.ChannelPush, notiftemplate.ColPushTitle, notiftemplate.ColPushBody); ok {
				title, err := renderColumn(fields, notiftemplate.ColPushTitle, vars)
				if err != nil {
					c.failed[cp.channel] = fmt.Errorf("%w: push: %w", ErrRenderFailed, err)
					continue
				}
				body, err := renderColumn(fields, notiftemplate.ColPushBody, vars)
				if err != nil {
					c.failed[cp.channel] = fmt.Errorf("%w: push: %w", ErrRenderFailed, err)
					continue
				}
				c.pushTitle, c.pushBody = cmp.Or(title, inApp.Title), body
			}
		}
	}
	return c
}
