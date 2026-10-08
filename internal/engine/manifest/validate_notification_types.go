package manifest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// validateNotificationTypes checks name uniqueness, required in_app availability and that
// default channels are a subset of available channels. Template files are validated during
// module loading.
func validateNotificationTypes(m Manifest) error {
	var violations []string
	reject := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf(format, args...))
	}

	seenNames := make(map[string]bool, len(m.NotificationTypes))
	for _, nt := range m.NotificationTypes {
		if seenNames[nt.Name] {
			reject("notification type %q: name must be unique within this manifest's notification_types", nt.Name)
		}
		seenNames[nt.Name] = true

		if !slices.Contains(nt.AvailableChannels, "in_app") {
			reject("notification type %q: available_channels must include \"in_app\"", nt.Name)
		}

		for _, ch := range nt.DefaultChannels {
			if !slices.Contains(nt.AvailableChannels, ch) {
				reject("notification type %q: default_channels value %q must be present in available_channels", nt.Name, ch)
			}
		}
	}

	if len(violations) == 0 {
		return nil
	}
	return errors.New(strings.Join(violations, "; "))
}
