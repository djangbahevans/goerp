package notify

import (
	"slices"

	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
)

// optionalChannels are the channels a preference switches; in_app has no
// preference and is always delivered.
var optionalChannels = []string{notifications.ChannelEmail, notifications.ChannelSMS, notifications.ChannelPush}

// routeInput is everything channel routing reads.
type routeInput struct {
	notificationType string
	// manifestDefaults and available are the type's default_channels and
	// available_channels.
	manifestDefaults []string
	available        []string
	prefs            *notifications.Preferences
	config           *notifconfig.Config
	force            []string
	additional       []string
}

// route returns requested channels in delivery order. Address/provider availability is
// checked afterward, including for module-forced channels.
func route(in routeInput) []string {
	// 1-2: the type's defaults, the tenant's notification_defaults entry
	// replacing the manifest's. Tenant config isn't validated against the
	// type, so its entry is held to available_channels here.
	set := map[string]bool{}
	for _, ch := range in.config.DefaultChannels(in.notificationType, in.manifestDefaults) {
		set[ch] = slices.Contains(in.available, ch)
	}

	// 3: the user's global preferences only opt out: a channel they turned
	// off goes, but one they left on is added only by the type's defaults.
	// 4: their preferences for this type, if they have any, decide each
	// channel outright, within available_channels.
	prefs, hasTypePrefs := in.prefs.Types[in.notificationType]
	if !hasTypePrefs {
		prefs = in.prefs.Global
	}
	userOn := map[string]bool{
		notifications.ChannelEmail: prefs.Email,
		notifications.ChannelSMS:   prefs.SMS,
		notifications.ChannelPush:  prefs.Push,
	}
	for ch, on := range userOn {
		if hasTypePrefs {
			set[ch] = on && slices.Contains(in.available, ch)
		} else if !on {
			set[ch] = false
		}
	}

	// 6: ForceChannel adds a channel whatever the user chose;
	// AdditionalChannel adds one unless the user has turned it off.
	for _, ch := range in.force {
		set[ch] = true
	}
	for _, ch := range in.additional {
		if on, ok := userOn[ch]; !ok || on {
			set[ch] = true
		}
	}

	// 7: in_app always.
	channels := []string{notifications.ChannelInApp}
	for _, ch := range optionalChannels {
		if set[ch] {
			channels = append(channels, ch)
		}
	}

	// 8: the kill switch is last, so nothing above can reinstate a channel
	// it removes.
	return in.config.ApplyKillSwitches(channels)
}
