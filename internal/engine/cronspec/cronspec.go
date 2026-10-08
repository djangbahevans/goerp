// Package cronspec parses the standard 5-field cron schedules of manifest
// cron_jobs (manifest-spec.md §16) and reports their next fire time in UTC.
package cronspec

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// The parser accepts exactly minute, hour, day of month, month and day of
// week: no seconds field and no descriptors such as "@daily".
var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

const fieldCount = 5

var weekdayNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// Schedule is a parsed cron expression.
type Schedule struct {
	schedule cron.Schedule
}

// Parse parses a 5-field cron expression: each field a comma-separated list
// of "*", values, ranges and "/step" forms, with month and weekday names and
// 7 as an alias for Sunday.
func Parse(expr string) (Schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != fieldCount {
		return Schedule{}, fmt.Errorf("schedule %q must have 5 fields (minute hour day month weekday), got %d", expr, len(fields))
	}
	for _, field := range fields {
		if err := checkSyntax(field); err != nil {
			return Schedule{}, fmt.Errorf("schedule %q: %w", expr, err)
		}
	}
	fields[4] = sundayAsZero(fields[4])

	schedule, err := parser.Parse(strings.Join(fields, " "))
	if err != nil {
		return Schedule{}, fmt.Errorf("schedule %q: %w", expr, err)
	}
	return Schedule{schedule: schedule}, nil
}

// Next returns the first fire time strictly after the given instant, in UTC,
// or the zero time when the schedule never fires, such as "0 0 31 2 *".
func (s Schedule) Next(after time.Time) time.Time {
	return s.schedule.Next(after.UTC())
}

// checkSyntax rejects what the parser tolerates beyond the documented grammar:
// signed numbers, "?" and empty list terms.
func checkSyntax(field string) error {
	if strings.Trim(field, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ*,/-") != "" {
		return fmt.Errorf("field %q has a character outside letters, digits and \"*,/-\"", field)
	}
	if slices.Contains(strings.Split(field, ","), "") {
		return fmt.Errorf("field %q has an empty list term", field)
	}
	return nil
}

// sundayAsZero rewrites a day-of-week field so that 7 means Sunday: the
// parser only accepts 0-6, so a term that mentions 7 is expanded into an
// explicit list of values with 7 folded onto 0.
func sundayAsZero(field string) string {
	terms := strings.Split(field, ",")
	for i, term := range terms {
		if expanded, ok := expandSundaySeven(term); ok {
			terms[i] = expanded
		}
	}
	return strings.Join(terms, ",")
}

// expandSundaySeven expands a single term whose range ends at or is 7. It
// reports false for a term it leaves to the parser, including malformed ones.
func expandSundaySeven(term string) (string, bool) {
	rangePart, stepPart, hasStep := strings.Cut(term, "/")
	lo, hi, isRange := strings.Cut(rangePart, "-")
	if lo != "7" && hi != "7" {
		return "", false
	}
	step := 1
	if hasStep {
		n, ok := unsigned(stepPart)
		if !ok || n < 1 {
			return "", false
		}
		step = n
	}
	if !isRange {
		return "0", true
	}
	loValue, ok := weekdayValue(lo)
	if !ok {
		return "", false
	}
	hiValue, ok := weekdayValue(hi)
	if !ok || loValue > hiValue {
		return "", false
	}
	var values []string
	for v := loValue; v <= hiValue; v += step {
		values = append(values, strconv.Itoa(v%7))
	}
	return strings.Join(values, ","), true
}

func weekdayValue(s string) (int, bool) {
	if n, ok := weekdayNames[strings.ToLower(s)]; ok {
		return n, true
	}
	return unsigned(s)
}

// unsigned parses a decimal without a sign, which strconv.Atoi would accept.
func unsigned(s string) (int, bool) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}
