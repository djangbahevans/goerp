package orm

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Domain builds a domain expression string (manifest-spec.md §8) from
// template, replacing each "?" with args[i] escaped/quoted per its Go
// type. Use this, never fmt.Sprintf or concatenation, for any domain
// incorporating a value from outside your own code — an unescaped value
// can widen a filter to match more than intended.
func Domain(template string, args ...any) string {
	var b strings.Builder
	argIdx := 0
	for i := range len(template) {
		if template[i] == '?' && argIdx < len(args) {
			b.WriteString(domainLiteral(args[argIdx]))
			argIdx++
			continue
		}
		b.WriteByte(template[i])
	}
	return b.String()
}

// domainLiteral renders v as a domain-language literal: a quoted string
// (embedded quotes doubled, SQL's own rule) — time.Time as RFC 3339,
// which Postgres parses directly — the true/false/null keywords, or
// plain digit text, never scientific notation, which the domain lexer
// doesn't parse. An unrecognized type falls back to a quoted string of
// its fmt.Sprint form.
func domainLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return quoteDomainString(x)
	case time.Time:
		return quoteDomainString(x.Format(time.RFC3339Nano))
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int8:
		return strconv.FormatInt(int64(x), 10)
	case int16:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint8:
		return strconv.FormatUint(uint64(x), 10)
	case uint16:
		return strconv.FormatUint(uint64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return quoteDomainString(fmt.Sprint(x))
	}
}

func quoteDomainString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
