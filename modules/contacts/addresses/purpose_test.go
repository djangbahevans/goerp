package addresses

import (
	"testing"
	"time"

	"github.com/djangbahevans/goerp/modules/contacts/models"
)

func TestForPurpose(t *testing.T) {
	created := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	primary := models.Address{ID: "primary", Type: "home", IsPrimary: true, CreatedAt: created.Add(time.Hour)}
	olderHome := models.Address{ID: "older-home", Type: "home", CreatedAt: created}
	billing := models.Address{ID: "b", Type: "billing", CreatedAt: created}
	tied := models.Address{ID: "a", Type: "billing", CreatedAt: created}
	archived := models.Address{ID: "archived", Type: "billing", IsPrimary: true, CreatedAt: created.Add(-time.Hour), DeletedAt: new(created)}

	for _, tc := range []struct {
		name    string
		records []models.Address
		purpose models.AddressType
		want    string
	}{
		{"primary of purpose", []models.Address{olderHome, billing, primary}, "home", "primary"},
		{"earliest of purpose", []models.Address{primary, billing}, "billing", "b"},
		{"creation tie", []models.Address{billing, primary, tied}, "billing", "a"},
		{"primary fallback", []models.Address{billing, primary}, "delivery", "primary"},
		{"archived ignored", []models.Address{archived, primary, billing}, "billing", "b"},
		{"matching without primary", []models.Address{billing}, "billing", "b"},
		{"unmatched without primary", []models.Address{billing}, "home", ""},
		{"only archived", []models.Address{archived}, "billing", ""},
		{"empty", nil, "billing", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ForPurpose(tc.records, tc.purpose)
			if got.ID != tc.want || ok != (tc.want != "") {
				t.Fatalf("ForPurpose = %s, %v; want %s", got.ID, ok, tc.want)
			}
		})
	}
}
