package l10n

import "testing"

func TestDirection(t *testing.T) {
	for _, tc := range []struct{ locale, want string }{
		{"en", "ltr"}, {"fr-GH", "ltr"}, {"ar-MA", "rtl"}, {"he", "rtl"},
		{"fa", "rtl"}, {"ur", "rtl"}, {"ps", "rtl"}, {"sd", "rtl"}, {"yi", "rtl"},
		{"dv", "rtl"}, {"ckb", "rtl"}, {"ku", "ltr"}, {"az-Arab", "rtl"}, {"ar-Latn", "ltr"},
		{"en-invalid-tag!", "ltr"},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			if got := Direction(tc.locale); got != tc.want {
				t.Errorf("direction = %q, want %q", got, tc.want)
			}
		})
	}
}
