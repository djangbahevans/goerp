package codegen

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/go-openapi/inflect"
)

// words splits s into its identifier words at every non-alphanumeric
// character ("order_line", "by-email", "Order Lines").
func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// pascal is s in PascalCase: each word's first letter upper-cased, the rest
// kept as written, so an already camelCased word ("bulkImport") keeps its
// inner capitals.
func pascal(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	return b.String()
}

// camel is s in camelCase.
func camel(s string) string {
	p := []rune(pascal(s))
	if len(p) == 0 {
		return ""
	}
	p[0] = unicode.ToLower(p[0])
	return string(p)
}

// resourceSegment is a qualified model name's last dotted segment.
func resourceSegment(qualified string) string {
	if _, after, ok := strings.CutLast(qualified, "."); ok {
		return after
	}
	return qualified
}

// modelTypeName is {Name}: the model's resource segment in PascalCase.
func modelTypeName(m Model) string {
	return pascal(resourceSegment(m.Name))
}

// modelPluralName is {Plural}: the model's LabelPlural in PascalCase, or its
// pluralized resource segment when it declares no LabelPlural.
func modelPluralName(m Model) string {
	if p := pascal(m.LabelPlural); p != "" {
		return p
	}
	return pascal(inflect.Pluralize(resourceSegment(m.Name)))
}

var identPattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// propertyKey renders name as an object-literal or interface key, quoted
// only when it isn't a plain identifier.
func propertyKey(name string) string {
	if identPattern.MatchString(name) {
		return name
	}
	return quote(name)
}

// quote renders s as a single-quoted TypeScript string literal.
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return "'" + s + "'"
}

// reservedWords are the JavaScript/TypeScript words a parameter can't be
// named.
var reservedWords = map[string]bool{
	"break": true, "case": true, "catch": true, "class": true, "const": true, "continue": true,
	"debugger": true, "default": true, "delete": true, "do": true, "else": true, "enum": true,
	"export": true, "extends": true, "false": true, "finally": true, "for": true, "function": true,
	"if": true, "import": true, "in": true, "instanceof": true, "new": true, "null": true,
	"return": true, "super": true, "switch": true, "this": true, "throw": true, "true": true,
	"try": true, "typeof": true, "var": true, "void": true, "while": true, "with": true,
	"yield": true, "let": true, "static": true, "implements": true, "interface": true,
	"package": true, "private": true, "protected": true, "public": true, "await": true,
	"arguments": true, "eval": true,
}

// paramName renders a path parameter as a function parameter name.
func paramName(s string) string {
	n := camel(s)
	if n == "" || unicode.IsDigit([]rune(n)[0]) {
		n = "p" + pascal(s)
	}
	if reservedWords[n] {
		n += "_"
	}
	return n
}
