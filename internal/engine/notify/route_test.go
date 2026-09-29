package notify

import (
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
)

const (
	inApp = notifications.ChannelInApp
	email = notifications.ChannelEmail
	sms   = notifications.ChannelSMS
	push  = notifications.ChannelPush
)

func allEnabled() *notifconfig.Config {
	return &notifconfig.Config{EmailEnabled: true, SMSEnabled: true, PushEnabled: true}
}

func noPrefs() *notifications.Preferences {
	return &notifications.Preferences{Global: notifications.DefaultChannels, Types: map[string]notifications.Channels{}}
}

func baseInput() routeInput {
	return routeInput{
		notificationType: "sales.order_confirmed",
		manifestDefaults: []string{inApp, email},
		available:        []string{inApp, email, sms, push},
		prefs:            noPrefs(),
		config:           allEnabled(),
	}
}

func TestRoute(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*routeInput)
		want   []string
	}{
		{
			name:   "manifest defaults without preferences",
			modify: func(*routeInput) {},
			want:   []string{inApp, email},
		},
		{
			name: "tenant default replaces the manifest's",
			modify: func(in *routeInput) {
				in.config.Defaults = map[string][]string{"sales.order_confirmed": {inApp, push}}
			},
			want: []string{inApp, push},
		},
		{
			name: "a tenant default cannot add an unavailable channel",
			modify: func(in *routeInput) {
				in.available = []string{inApp, email}
				in.config.Defaults = map[string][]string{"sales.order_confirmed": {inApp, email, sms}}
			},
			want: []string{inApp, email},
		},
		{
			name: "a global preference left on does not add a channel the defaults leave out",
			modify: func(in *routeInput) {
				in.config.Defaults = map[string][]string{"sales.order_confirmed": {inApp}}
				in.prefs.Global = notifications.Channels{Email: true, SMS: true, Push: true}
			},
			want: []string{inApp},
		},
		{
			name: "a global preference turned off removes a default channel",
			modify: func(in *routeInput) {
				in.prefs.Global = notifications.Channels{Push: true}
			},
			want: []string{inApp},
		},
		{
			name: "type preference beats the global one",
			modify: func(in *routeInput) {
				in.prefs.Global = notifications.Channels{}
				in.prefs.Types["sales.order_confirmed"] = notifications.Channels{Email: true, SMS: true}
			},
			want: []string{inApp, email, sms},
		},
		{
			name: "another type's preference is ignored",
			modify: func(in *routeInput) {
				in.prefs.Types["hr.leave_approved"] = notifications.Channels{}
			},
			want: []string{inApp, email},
		},
		{
			name: "a preference cannot enable an unavailable channel",
			modify: func(in *routeInput) {
				in.available = []string{inApp, email}
				in.prefs.Types["sales.order_confirmed"] = notifications.Channels{Email: true, Push: true}
			},
			want: []string{inApp, email},
		},
		{
			name: "in_app is kept when every preference is off",
			modify: func(in *routeInput) {
				in.prefs.Global = notifications.Channels{}
			},
			want: []string{inApp},
		},
		{
			name: "ForceChannel overrides a disabled preference",
			modify: func(in *routeInput) {
				in.prefs.Types["sales.order_confirmed"] = notifications.Channels{}
				in.force = []string{sms}
			},
			want: []string{inApp, sms},
		},
		{
			name: "AdditionalChannel adds a channel the user has not turned off",
			modify: func(in *routeInput) {
				in.additional = []string{push}
			},
			want: []string{inApp, email, push},
		},
		{
			name: "AdditionalChannel does not override a disabled preference",
			modify: func(in *routeInput) {
				in.prefs.Types["sales.order_confirmed"] = notifications.Channels{Email: true}
				in.additional = []string{push}
			},
			want: []string{inApp, email},
		},
		{
			name: "AdditionalChannel does not override a global opt-out",
			modify: func(in *routeInput) {
				in.additional = []string{sms}
			},
			want: []string{inApp, email},
		},
		{
			name: "AdditionalChannel follows the type preference over the global one",
			modify: func(in *routeInput) {
				in.prefs.Types["sales.order_confirmed"] = notifications.Channels{Email: true, SMS: true}
				in.additional = []string{sms}
			},
			want: []string{inApp, email, sms},
		},
		{
			name: "kill switch removes a channel the user enabled and the module forced",
			modify: func(in *routeInput) {
				in.config.EmailEnabled = false
				in.prefs.Types["sales.order_confirmed"] = notifications.Channels{Email: true}
				in.force = []string{email}
				in.additional = []string{email}
			},
			want: []string{inApp},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := baseInput()
			tt.modify(&in)
			if got := route(in); !slices.Equal(got, tt.want) {
				t.Errorf("route() = %v, want %v", got, tt.want)
			}
		})
	}
}
