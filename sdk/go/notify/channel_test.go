package notify

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestChannelConstantsHaveChannelType(t *testing.T) {
	for _, channel := range []any{ChannelInApp, ChannelEmail, ChannelSMS, ChannelPush} {
		if reflect.TypeOf(channel) != reflect.TypeFor[Channel]() {
			t.Errorf("%v has type %T, want Channel", channel, channel)
		}
	}
}

func TestChannelArgumentsRejectUnrelatedTypes(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "build", "-gcflags=-e", "-o", filepath.Join(t.TempDir(), "wrongchannels"), "./testdata/wrongchannels")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("go build succeeded for unrelated channel types")
	}

	for _, api := range []string{"DefaultChannels", "AvailableChannels", "Template", "ForceChannel", "AdditionalChannel"} {
		for _, argument := range []string{"channel (variable of type string)", "unrelated (variable of string type unrelatedChannel)"} {
			matched := false
			for line := range strings.SplitSeq(string(output), "\n") {
				if strings.Contains(line, "cannot use "+argument) && strings.Contains(line, "as notify.Channel value in argument to notify."+api) {
					matched = true
					break
				}
			}

			if !matched {
				t.Errorf("missing %s type error for %s:\n%s", api, argument, output)
			}
		}
	}
}

func TestDefineRejectsExplicitUnsupportedChannels(t *testing.T) {
	for name, option := range map[string]DefineOption{
		"default":   DefaultChannels(ChannelInApp, Channel("pager")),
		"available": AvailableChannels(ChannelInApp, Channel("pager")),
		"template":  Template(Channel("pager"), "pager.{locale}.txt"),
	} {
		t.Run(name, func(t *testing.T) {
			mustPanic(t, name, func() {
				Define[struct{}]("unsupported_"+name, Label("Unsupported"), option)
			})
		})
	}
}
