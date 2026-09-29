package notify

import (
	"slices"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestBuildOptions(t *testing.T) {
	got := buildOptions([]NotifyOption{
		HighPriority(),
		ForceChannel(ChannelEmail),
		ForceChannel(ChannelSMS),
		AdditionalChannel(ChannelPush),
		AdditionalChannel(ChannelEmail),
		WithIdempotencyKey("order-confirmed:o-1"),
		WithActionURL("/_m/sales/orders/o-1"),
	})
	want := abi.NotifySendOptions{
		Priority: "high", ChannelOverride: ChannelSMS, AdditionalChannels: []string{ChannelPush, ChannelEmail},
		IdempotencyKey: "order-confirmed:o-1", ActionURL: "/_m/sales/orders/o-1",
	}
	if got.Priority != want.Priority || got.ChannelOverride != want.ChannelOverride || !slices.Equal(got.AdditionalChannels, want.AdditionalChannels) ||
		got.IdempotencyKey != want.IdempotencyKey || got.ActionURL != want.ActionURL {
		t.Fatalf("buildOptions = %+v, want %+v", got, want)
	}
}

func TestBuildOptions_LaterPriorityWins(t *testing.T) {
	if got := buildOptions([]NotifyOption{HighPriority(), NormalPriority()}); got.Priority != "normal" {
		t.Fatalf("Priority = %q, want normal", got.Priority)
	}
}

func TestBuildOptions_NoneLeavesZeroValue(t *testing.T) {
	got := buildOptions(nil)
	if got.Priority != "" || got.ChannelOverride != "" || got.AdditionalChannels != nil || got.IdempotencyKey != "" || got.ActionURL != "" {
		t.Fatalf("buildOptions(nil) = %+v, want zero value", got)
	}
}
