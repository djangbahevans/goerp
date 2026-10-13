package modeltest

import "testing"

func TestHarnessTranslations(t *testing.T) {
	t.Chdir("testdata/localefixture")
	h := NewHarness(t, WithLocale("fr-GH"))
	response := h.GET("/localeprobe/translations")
	if response.StatusCode != 200 {
		t.Fatalf("response = %d: %v", response.StatusCode, response.JSON("error"))
	}
	for key, want := range map[string]string{
		"hello":    "Bonjour Kofi",
		"named":    "Bonjour Kofi",
		"english":  "English",
		"explicit": "Hello Kofi",
		"zero":     "0 commandes",
		"one":      "One order",
		"other":    "5 commandes",
		"missing":  "missing",
	} {
		if got := response.JSON(key); got != want {
			t.Errorf("%s = %v, want %q", key, got, want)
		}
	}
	response = h.WithLocale("en").GET("/localeprobe/translations")
	if got := response.JSON("zero"); got != "No orders" {
		t.Errorf("English zero form = %v, want No orders", got)
	}
}
