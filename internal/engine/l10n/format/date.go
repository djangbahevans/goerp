package format

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/agentable/go-intl/datetimeformat"
	"github.com/agentable/go-intl/locale"
	"github.com/agentable/go-intl/numberformat"
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

func Date(input abi.FormatDateInput) (string, error) {
	if input.Timezone == "" || input.Timezone == "Local" {
		return "", fmt.Errorf("timezone must be an IANA timezone")
	}
	zone, err := time.LoadLocation(input.Timezone)
	if err != nil {
		return "", fmt.Errorf("load timezone: %w", err)
	}
	locales, err := locale.ParseList(input.Locale)
	if err != nil {
		return "", err
	}
	t := time.UnixMilli(input.UnixMS).In(zone)
	pattern := cmp.Or(input.Format, "medium")
	switch pattern {
	case "short", "medium", "long", "full":
		options := datetimeformat.Options{DateStyle: new(pattern), TimeZone: new(input.Timezone), Calendar: new("gregory")}
		if input.IncludeTime {
			options.TimeStyle = new(pattern)
		}
		formatter, err := datetimeformat.New(locales, options)
		if err != nil {
			return "", err
		}
		return formatter.Format(t)
	default:
		if input.IncludeTime {
			return "", fmt.Errorf("include_time requires a named style")
		}
		return datePattern(t, pattern, locales, input.Timezone)
	}
}

func datePattern(t time.Time, pattern string, locales locale.List, timezone string) (string, error) {
	if len(pattern) > 256 {
		return "", fmt.Errorf("date pattern exceeds 256 bytes")
	}
	var result strings.Builder
	quoted := false
	fields := make(map[string]string)
	for i := 0; i < len(pattern); {
		ch := pattern[i]
		if ch == '\'' {
			if i+1 < len(pattern) && pattern[i+1] == '\'' {
				result.WriteByte('\'')
				i += 2
				continue
			}
			quoted = !quoted
			i++
			continue
		}
		if quoted || (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') {
			result.WriteByte(ch)
			i++
			continue
		}
		end := i + 1
		for end < len(pattern) && pattern[end] == ch {
			end++
		}
		token := pattern[i:end]
		value, ok := fields[token]
		if !ok {
			var err error
			value, err = dateField(t, token, locales, timezone)
			if err != nil {
				return "", err
			}
			fields[token] = value
		}
		result.WriteString(value)
		i = end
	}
	if quoted {
		return "", fmt.Errorf("unterminated date pattern literal")
	}
	return result.String(), nil
}

func dateField(t time.Time, token string, locales locale.List, timezone string) (string, error) {
	ch, width := token[0], len(token)
	options := datetimeformat.Options{TimeZone: new(timezone), Calendar: new("gregory")}
	partType := ""
	numeric := -1
	switch ch {
	case 'y':
		if width > 6 {
			break
		}
		numeric = t.Year()
		if numeric <= 0 {
			numeric = 1 - numeric
		}
		if width == 2 {
			numeric %= 100
		}
	case 'M', 'L':
		if width <= 2 {
			numeric = int(t.Month())
		} else if width <= 5 {
			options.Month = new([]string{"short", "long", "narrow"}[width-3])
			if ch == 'M' {
				options.Day = new("numeric")
			}
			partType = "month"
		}
	case 'd':
		if width <= 2 {
			numeric = t.Day()
		}
	case 'H', 'h', 'K', 'k':
		if width <= 2 {
			numeric = t.Hour()
			switch ch {
			case 'h':
				numeric = (numeric+11)%12 + 1
			case 'K':
				numeric %= 12
			case 'k':
				if numeric == 0 {
					numeric = 24
				}
			}
		}
	case 'm':
		if width <= 2 {
			numeric = t.Minute()
		}
	case 's':
		if width <= 2 {
			numeric = t.Second()
		}
	case 'S':
		if width <= 3 {
			numeric, _ = strconv.Atoi(fmt.Sprintf("%09d", t.Nanosecond())[:width])
		}
	case 'E':
		if width <= 5 {
			style := "short"
			switch width {
			case 4:
				style = "long"
			case 5:
				style = "narrow"
			}
			options.Weekday = new(style)
			partType = "weekday"
		}
	case 'a':
		if width <= 3 {
			options.Hour, options.HourCycle = new("numeric"), new("h12")
			partType = "dayPeriod"
		}
	case 'G':
		if width <= 5 {
			style := "short"
			switch width {
			case 4:
				style = "long"
			case 5:
				style = "narrow"
			}
			options.Era = new(style)
			partType = "era"
		}
	case 'z':
		if width <= 4 {
			style := "short"
			if width == 4 {
				style = "long"
			}
			options.TimeZoneName = new(style)
			partType = "timeZoneName"
		}
	case 'X', 'x':
		if width <= 3 {
			_, offset := t.Zone()
			if offset == 0 && ch == 'X' {
				return "Z", nil
			}
			sign := "+"
			if offset < 0 {
				sign, offset = "-", -offset
			}
			hours, minutes := offset/3600, offset%3600/60
			if width == 1 && minutes == 0 {
				return fmt.Sprintf("%s%02d", sign, hours), nil
			}
			separator := ""
			if width == 3 {
				separator = ":"
			}
			return fmt.Sprintf("%s%02d%s%02d", sign, hours, separator, minutes), nil
		}
	}
	if numeric >= 0 {
		formatter, err := numberformat.New(locales, numberformat.Options{UseGrouping: new("false"), MinimumIntegerDigits: new(width), MaximumFractionDigits: new(0)})
		if err != nil {
			return "", err
		}
		return formatter.Format(numberformat.Int(int64(numeric))), nil
	}
	if partType == "" {
		return "", fmt.Errorf("unsupported CLDR date field %q", token)
	}
	formatter, err := datetimeformat.New(locales, options)
	if err != nil {
		return "", err
	}
	parts, err := formatter.FormatToParts(t)
	if err != nil {
		return "", err
	}
	index := slices.IndexFunc(parts, func(part datetimeformat.Part) bool {
		return string(part.Type) == partType
	})
	if index < 0 {
		return "", fmt.Errorf("CLDR date field %q has no value", token)
	}
	return parts[index].Value, nil
}
