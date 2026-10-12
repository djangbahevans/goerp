package l10n

import "golang.org/x/text/language"

// Direction follows the locale's script, including an explicit script subtag.
func Direction(locale string) string {
	tag, err := language.Parse(locale)
	if err != nil {
		return "ltr"
	}
	script, _ := tag.Script()
	switch script.String() {
	case "Arab", "Hebr", "Thaa", "Syrc", "Nkoo", "Adlm", "Rohg", "Samr", "Mand":
		return "rtl"
	default:
		return "ltr"
	}
}
