package recordactivity

import (
	"slices"
	"testing"
)

const (
	amaID   = "0196f3a2-0000-7000-8000-000000000001"
	kwameID = "0196f3a2-0000-7000-8000-000000000002"
)

func TestMentionIDs_DistinctInOrderOfFirstAppearance(t *testing.T) {
	body := "<@" + kwameID + "> and <@" + amaID + ">, <@" + kwameID + "> again"
	if got := MentionIDs(body); !slices.Equal(got, []string{kwameID, amaID}) {
		t.Errorf("MentionIDs() = %v, want [kwame ama]", got)
	}
}

func TestMentionIDs_OnlyExactTokensAreMentions(t *testing.T) {
	for _, body := range []string{
		"ama@acme.example",
		"@" + amaID,
		"<@" + amaID,
		"<@0196F3A2-0000-7000-8000-000000000001>",
		"<@ " + amaID + ">",
		"<@not-a-uuid>",
	} {
		if got := MentionIDs(body); len(got) != 0 {
			t.Errorf("MentionIDs(%q) = %v, want none", body, got)
		}
	}
}

func TestRenderMentions_ReplacesEachTokenWithItsLabel(t *testing.T) {
	names := map[string]string{amaID: "Ama Owusu", kwameID: "kwame"}
	got := RenderMentions("<@"+amaID+"> ask <@"+kwameID+"> (<@"+amaID+">)", func(id string) string { return names[id] })
	if want := "@Ama Owusu ask @kwame (@Ama Owusu)"; got != want {
		t.Errorf("RenderMentions() = %q, want %q", got, want)
	}
}
