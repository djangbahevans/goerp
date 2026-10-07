package registry

import (
	"strconv"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// staticDefault reads a field's Default expression as the value a create would
// store, for the kinds the shell can show before the first save: a quoted
// string, a boolean or a number, optionally cast (`'draft'::text`). An
// expression that calls a function or reads another column has no static value.
func staticDefault(kind model.FieldKind, expr string) (any, bool) {
	expr = withoutCast(strings.TrimSpace(expr))
	switch kind {
	case model.KindChar, model.KindText, model.KindSelection, model.KindEnum:
		if value, ok := quotedString(expr); ok {
			return value, true
		}
	case model.KindDate:
		if value, ok := quotedString(expr); ok && isISODate(value) {
			return value, true
		}
	case model.KindBoolean:
		switch strings.ToLower(expr) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	case model.KindInteger, model.KindBigInt:
		if n, err := strconv.ParseInt(expr, 10, 64); err == nil {
			return n, true
		}
	case model.KindFloat:
		if f, err := strconv.ParseFloat(expr, 64); err == nil && isPlainNumber(expr) {
			return f, true
		}
	case model.KindDecimal:
		if isPlainNumber(expr) {
			return expr, true
		}
	}
	return nil, false
}

// withoutCast drops a trailing `::type`, which only names the literal's type.
func withoutCast(expr string) string {
	value, typeName, ok := strings.CutLast(expr, "::")
	if !ok || value == "" || typeName == "" || !isTypeName(typeName) {
		return expr
	}
	return value
}

// isPlainNumber is an optionally signed run of digits with an optional fraction, so NaN, Inf and hex floats are not numbers here.
func isPlainNumber(s string) bool {
	if rest, ok := strings.CutPrefix(s, "-"); ok {
		s = rest
	} else if rest, ok := strings.CutPrefix(s, "+"); ok {
		s = rest
	}
	whole, fraction, hasFraction := strings.Cut(s, ".")
	return allDigits(whole) && (!hasFraction || allDigits(fraction))
}

func allDigits(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' })
}

// isISODate is a calendar date as 2026-01-31, not an input such as 'today' or 'infinity'.
func isISODate(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

func isTypeName(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == ' ':
		default:
			return false
		}
	}
	return true
}

// quotedString unquotes a single SQL string literal, whose only escape is a doubled quote.
func quotedString(expr string) (string, bool) {
	inner, ok := strings.CutPrefix(expr, "'")
	if !ok {
		return "", false
	}
	inner, ok = strings.CutSuffix(inner, "'")
	if !ok || strings.Contains(strings.ReplaceAll(inner, "''", ""), "'") {
		return "", false
	}
	return strings.ReplaceAll(inner, "''", "'"), true
}
