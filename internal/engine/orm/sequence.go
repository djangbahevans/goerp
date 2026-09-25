// Package orm holds reusable ORM primitives shared by host.orm's WASM
// dispatch shims (internal/engine/wasm) rather than living inside them.
package orm

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// ResolvePeriodKey substitutes period tokens in format against at,
// producing the period_key a Sequence field's counter is scoped to.
// Supported tokens: {year} (4-digit), {month} (2-digit, zero-padded),
// {day} (2-digit, zero-padded). A format with no tokens resolves to
// itself verbatim, giving a single counter that never rolls over.
func ResolvePeriodKey(format string, at time.Time) string {
	r := strings.NewReplacer(
		"{year}", strconv.Itoa(at.Year()),
		"{month}", fmt.Sprintf("%02d", at.Month()),
		"{day}", fmt.Sprintf("%02d", at.Day()),
	)
	return r.Replace(format)
}

var (
	seqToken         = regexp.MustCompile(`\{seq:(\d{1,2})\}`)
	placeholderToken = regexp.MustCompile(`\{[^{}]*\}`)
)

// maxSeqWidth fits the widest int64 counter.
const maxSeqWidth = 20

// ValidateSequenceFormat checks a Sequence field's format: its only
// placeholders are {year}, {month} and {seq:N}, and it has exactly one
// {seq:N}, without which every record in a period would get the same value.
func ValidateSequenceFormat(format string) error {
	seqs := 0
	for _, token := range placeholderToken.FindAllString(format, -1) {
		switch {
		case token == "{year}", token == "{month}":
		case seqToken.MatchString(token):
			if width, _ := strconv.Atoi(seqToken.FindStringSubmatch(token)[1]); width < 1 || width > maxSeqWidth {
				return fmt.Errorf("sequence format %q: %s pads to %d digits, want 1 to %d", format, token, width, maxSeqWidth)
			}
			seqs++
		default:
			return fmt.Errorf("sequence format %q: unknown placeholder %s, want {year}, {month} or {seq:N}", format, token)
		}
	}
	if seqs != 1 {
		return fmt.Errorf("sequence format %q: has %d {seq:N} placeholders, want exactly one", format, seqs)
	}
	return nil
}

// FormatSequence renders a Sequence field's stored value: format's period
// tokens resolved against at (ResolvePeriodKey) and {seq:N} replaced by n
// zero-padded to N digits.
func FormatSequence(format string, at time.Time, n int64) string {
	return seqToken.ReplaceAllStringFunc(ResolvePeriodKey(format, at), func(token string) string {
		width, _ := strconv.Atoi(seqToken.FindStringSubmatch(token)[1])
		return fmt.Sprintf("%0*d", width, n)
	})
}

// AcquireNext atomically increments and returns the next counter value
// for (modelName, field, periodKey) in the given tenant schema, scoped to
// tx — the same open transaction the caller's host.db.begin registered.
// It must never open its own sub-transaction: a caller rollback undoes
// the increment along with the rest of the transaction, leaving no
// permanent gap in the sequence.
func AcquireNext(ctx context.Context, tx *sql.Tx, tenantSlug, modelName, field, periodKey string) (int64, error) {
	query := fmt.Sprintf(`
		INSERT INTO %s.sequences (model, field, period_key, next_value)
		VALUES ($1, $2, $3, 1)
		ON CONFLICT (model, field, period_key)
		DO UPDATE SET next_value = sequences.next_value + 1
		RETURNING next_value
	`, tenantschema.Name(tenantSlug))

	var next int64
	if err := tx.QueryRowContext(ctx, query, modelName, field, periodKey).Scan(&next); err != nil {
		return 0, fmt.Errorf("acquire sequence %s.%s[%s]: %w", modelName, field, periodKey, err)
	}
	return next, nil
}
