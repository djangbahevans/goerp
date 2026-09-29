// Package enginenotif declares the notification types the engine sends
// itself (notification-system.md §6 "Engine-declared notification types")
// and their default templates, which ship embedded in the engine binary.
// It sits below both notify, which sends them, and registry, which
// resolves their templates and lists them in GET /_meta/schema.
package enginenotif

import (
	"embed"
	"fmt"
	"io/fs"
	"sync"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
)

// Module is the reserved module name the engine's types are declared
// under, so their full type strings are "engine.{name}".
const Module = manifest.ReservedEngineName

// Names of the engine's types, without the "engine." prefix.
const (
	ActivityAssigned = "activity_assigned"
	ActivityDue      = "activity_due"
	RecordMention    = "record_mention"
	RecordMessage    = "record_message"
)

var (
	assignableChannels = []string{notifications.ChannelInApp, notifications.ChannelEmail, notifications.ChannelPush}
	inAppAndEmail      = []string{notifications.ChannelInApp, notifications.ChannelEmail}
)

// Types are the engine's notification types, with the same fields a
// manifest's notification_types entry has.
var Types = []manifest.NotificationType{
	{
		Name: ActivityAssigned, Label: "Activity assigned to you",
		Description:     "Sent when someone assigns you a scheduled activity",
		DefaultChannels: inAppAndEmail, AvailableChannels: assignableChannels,
		Templates: map[string]string{notifications.ChannelInApp: "templates/activity_assigned/in_app.{locale}.json"},
	},
	{
		Name: ActivityDue, Label: "Activity due today",
		Description:     "Sent the morning an activity assigned to you falls due",
		DefaultChannels: []string{notifications.ChannelInApp}, AvailableChannels: assignableChannels,
		Templates: map[string]string{notifications.ChannelInApp: "templates/activity_due/in_app.{locale}.json"},
	},
	{Name: RecordMention, Label: "Mentioned you in a comment", DefaultChannels: inAppAndEmail, AvailableChannels: assignableChannels},
	{Name: RecordMessage, Label: "Message on a record you follow", DefaultChannels: inAppAndEmail, AvailableChannels: assignableChannels},
}

//go:embed templates
var templateFS embed.FS

// Templates returns the engine types' default templates, parsed once.
// They are part of the binary, so a template that fails to load is a
// build defect, caught by this package's tests.
var Templates = sync.OnceValue(func() *notiftemplate.ModuleTemplates {
	t, err := loadTemplates(templateFS)
	if err != nil {
		panic(fmt.Sprintf("enginenotif: embedded templates: %v", err))
	}
	return t
})

func loadTemplates(fsys fs.FS) (*notiftemplate.ModuleTemplates, error) {
	return notiftemplate.LoadFS(Types, fsys)
}
