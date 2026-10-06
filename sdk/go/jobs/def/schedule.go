package def

import (
	"fmt"
	"strconv"
	"strings"
)

// scheduleField describes one field of a 5-field cron expression: its
// inclusive bounds and the names it accepts in place of numbers.
type scheduleField struct {
	name     string
	min, max int
	names    []string
	nameBase int
}

var scheduleFields = [5]scheduleField{
	{name: "minute", min: 0, max: 59},
	{name: "hour", min: 0, max: 23},
	{name: "day of month", min: 1, max: 31},
	{name: "month", min: 1, max: 12, names: []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}, nameBase: 1},
	{name: "day of week", min: 0, max: 7, names: []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}},
}

// validateSchedule accepts the standard 5-field cron form: each field a
// comma-separated list of "*", a value or a range, with an optional "/step".
// Descriptors such as "@daily" and a seconds field are rejected.
func validateSchedule(expr string) error {
	parts := strings.Fields(expr)
	if len(parts) != len(scheduleFields) {
		return fmt.Errorf("schedule %q must have 5 fields (minute hour day month weekday), got %d", expr, len(parts))
	}
	for i, part := range parts {
		if err := scheduleFields[i].validate(part); err != nil {
			return fmt.Errorf("schedule %q: %w", expr, err)
		}
	}
	return nil
}

func (f scheduleField) validate(field string) error {
	for term := range strings.SplitSeq(field, ",") {
		if err := f.validateTerm(term); err != nil {
			return fmt.Errorf("%s field %q: %w", f.name, field, err)
		}
	}
	return nil
}

func (f scheduleField) validateTerm(term string) error {
	rangePart, step, hasStep := strings.Cut(term, "/")
	if hasStep {
		n, ok := atoi(step)
		if !ok || n < 1 {
			return fmt.Errorf("step %q must be a positive integer", step)
		}
	}

	if rangePart == "*" {
		return nil
	}

	lo, hi, isRange := strings.Cut(rangePart, "-")
	loVal, err := f.value(lo)
	if err != nil {
		return err
	}
	if !isRange {
		return nil
	}
	hiVal, err := f.value(hi)
	if err != nil {
		return err
	}
	if loVal > hiVal {
		return fmt.Errorf("range %q is descending", rangePart)
	}
	return nil
}

func (f scheduleField) value(s string) (int, error) {
	if i := indexFold(f.names, s); i >= 0 {
		return i + f.nameBase, nil
	}
	n, ok := atoi(s)
	if !ok {
		return 0, fmt.Errorf("%q is not a number or a name", s)
	}
	if n < f.min || n > f.max {
		return 0, fmt.Errorf("%d is outside %d-%d", n, f.min, f.max)
	}
	return n, nil
}

func indexFold(names []string, s string) int {
	for i, name := range names {
		if strings.EqualFold(name, s) {
			return i
		}
	}
	return -1
}

// atoi parses an unsigned decimal; strconv.Atoi would also accept a sign.
func atoi(s string) (int, bool) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}
