package notify

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/registry"
)

// EngineModule is the reserved module name the engine's own notification
// types are declared under (notification-system.md §6 "Engine-declared
// notification types").
const EngineModule = manifest.ReservedEngineName

var (
	// ErrUndeclaredType: the notification type is not one the emitting
	// module declares (or, for the engine, one EngineTypes lists).
	ErrUndeclaredType = errors.New("notification type is not declared by the emitting module")
	// ErrUnknownChannel: an option names something that is not a channel.
	ErrUnknownChannel = errors.New("unknown notification channel")
)

var (
	engineAssignableChannels = []string{notifications.ChannelInApp, notifications.ChannelEmail, notifications.ChannelPush}
	inAppAndEmail            = []string{notifications.ChannelInApp, notifications.ChannelEmail}
)

// EngineTypes are the notification types the engine sends itself, with the
// same fields a manifest's notification_types entry has. Their full type
// strings are "engine.{name}".
var EngineTypes = []manifest.NotificationType{
	{Name: "activity_assigned", Label: "Activity assigned to you", DefaultChannels: inAppAndEmail, AvailableChannels: engineAssignableChannels},
	{Name: "activity_due", Label: "Activity due today", DefaultChannels: []string{notifications.ChannelInApp}, AvailableChannels: engineAssignableChannels},
	{Name: "record_mention", Label: "Mentioned you in a comment", DefaultChannels: inAppAndEmail, AvailableChannels: engineAssignableChannels},
	{Name: "record_message", Label: "Message on a record you follow", DefaultChannels: inAppAndEmail, AvailableChannels: engineAssignableChannels},
}

// lookupType is routing's step 0: the declaration of notificationType
// ("{module}.{name}") as moduleName emits it. An engine type is looked up
// in EngineTypes and never in any module's manifest; a module type must be
// the module's own and declared in its manifest.
func lookupType(snapshot *registry.RegistrySnapshot, moduleName, notificationType string) (manifest.NotificationType, error) {
	prefix, name, ok := strings.Cut(notificationType, ".")
	if !ok || prefix != moduleName || name == "" {
		return manifest.NotificationType{}, fmt.Errorf("%w: %q is not a %s notification type", ErrUndeclaredType, notificationType, moduleName)
	}

	declared := EngineTypes
	if moduleName != EngineModule {
		var mod *module.LoadedModule
		if snapshot != nil {
			mod = snapshot.Modules()[moduleName]
		}
		if mod == nil || mod.Status == module.StatusFailed {
			return manifest.NotificationType{}, fmt.Errorf("%w: module %q is not loaded", ErrUndeclaredType, moduleName)
		}
		declared = mod.Manifest.NotificationTypes
	}

	i := slices.IndexFunc(declared, func(nt manifest.NotificationType) bool { return nt.Name == name })
	if i < 0 {
		return manifest.NotificationType{}, fmt.Errorf("%w: %q", ErrUndeclaredType, notificationType)
	}
	return declared[i], nil
}

func validateChannels(channels []string) error {
	for _, ch := range channels {
		if ch != notifications.ChannelInApp && !slices.Contains(optionalChannels, ch) {
			return fmt.Errorf("%w: %q", ErrUnknownChannel, ch)
		}
	}
	return nil
}
