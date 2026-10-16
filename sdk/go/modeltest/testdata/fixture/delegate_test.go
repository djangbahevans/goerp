package fixture

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/modeltest"
	"github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/fixture/models"
)

func TestDelegateComposition(t *testing.T) {
	h := modeltest.NewHarness(t)
	h.Model.AssertField("widgets.profile", "card_id", modeltest.FieldOpts{Type: "delegate", RelatedModel: "widgets.card"})

	created := h.POST("/widgets/profiles", map[string]any{"name": "Kwame", "department": "Sales"})
	if created.StatusCode != 201 || created.JSON("name") != "Kwame" {
		t.Fatalf("create profile: status=%d, body=%v", created.StatusCode, created.JSON(""))
	}
	var profile models.Profile
	if err := profile.Scan(map[string]any{"name": created.JSON("name")}); err != nil || profile.Name == nil || *profile.Name != "Kwame" {
		t.Fatalf("generated model lost delegated fields: %+v, %v", profile, err)
	}
	id := created.JSON("id").(string)
	cardID := created.JSON("card_id").(string)
	h.DB.AssertExists("widgets_cards", map[string]any{"id": cardID, "name": "Kwame"})
	h.DB.AssertExists("widgets_profiles", map[string]any{"id": id, "department": "Sales"})

	read := h.GET("/widgets/profiles/" + id)
	if read.StatusCode != 200 || read.JSON("name") != "Kwame" || read.JSON("id") != id {
		t.Fatalf("get profile: status=%d, name=%v", read.StatusCode, read.JSON("name"))
	}
	updated := h.PUT("/widgets/profiles/"+id, map[string]any{"name": "Ama", "department": "Support"})
	if updated.StatusCode != 200 {
		t.Fatalf("update profile: status=%d, error=%v", updated.StatusCode, updated.JSON("error"))
	}
	h.DB.AssertExists("widgets_cards", map[string]any{"id": cardID, "name": "Ama"})
	h.DB.AssertExists("widgets_profiles", map[string]any{"id": id, "department": "Support"})
}
