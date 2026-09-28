// Package ipallowlist is a tenant's login IP allowlist (shell-ux.md §5.5
// "Security"): comma-separated CIDR ranges in tenantconfig's
// auth.ip_allowlist key. Empty, the default, allows every address. It
// restricts where users sign in to the tenant from, and is separate from
// an API key's own per-key allowlist (auth-internals.md §7).
package ipallowlist

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

const Key = "auth.ip_allowlist"

// maxEntries bounds the list the login flow scans on every attempt.
const maxEntries = 256

// Parse reads a comma-separated list of CIDR ranges. A bare address is a
// single-host range, and host bits are masked off, so "10.1.2.3/8" is
// 10.0.0.0/8. An IPv4-mapped IPv6 entry (::ffff:203.0.113.5) becomes its
// IPv4 form, since Allows compares client addresses unmapped. Blank
// entries are skipped; duplicates are dropped.
func Parse(s string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for entry := range strings.SplitSeq(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		var p netip.Prefix
		if strings.Contains(entry, "/") {
			parsed, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("%q is not a CIDR range", entry)
			}
			if parsed.Addr().Is4In6() && parsed.Bits() >= 96 {
				parsed = netip.PrefixFrom(parsed.Addr().Unmap(), parsed.Bits()-96)
			}
			p = parsed.Masked()
		} else {
			addr, err := netip.ParseAddr(entry)
			if err != nil || addr.Zone() != "" {
				return nil, fmt.Errorf("%q is not an IP address or CIDR range", entry)
			}
			addr = addr.Unmap()
			p = netip.PrefixFrom(addr, addr.BitLen())
		}
		if !slices.Contains(prefixes, p) {
			prefixes = append(prefixes, p)
		}
	}
	if len(prefixes) > maxEntries {
		return nil, fmt.Errorf("at most %d ranges are allowed", maxEntries)
	}
	return prefixes, nil
}

// Format is Parse's inverse: the canonical comma-separated form.
func Format(prefixes []netip.Prefix) string {
	parts := make([]string, len(prefixes))
	for i, p := range prefixes {
		parts[i] = p.String()
	}
	return strings.Join(parts, ",")
}

// Allows reports whether ip falls in any of prefixes; an empty list
// allows every address. An unparseable ip is never allowed by a
// non-empty list.
func Allows(prefixes []netip.Prefix, ip string) bool {
	if len(prefixes) == 0 {
		return true
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	addr = addr.WithZone("").Unmap()
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

type Store struct {
	config *tenantconfig.Store
}

func NewStore(config *tenantconfig.Store) *Store {
	return &Store{config: config}
}

// Load reads tenantID's allowlist. A stored value that doesn't parse is
// an error rather than an empty list, so a login check fails closed
// instead of silently dropping the restriction.
func (s *Store) Load(ctx context.Context, tenantID string) ([]netip.Prefix, error) {
	v, _, err := s.config.Get(ctx, tenantID, Key)
	if err != nil {
		return nil, fmt.Errorf("load ip allowlist: %w", err)
	}
	prefixes, err := Parse(v)
	if err != nil {
		return nil, fmt.Errorf("parse stored ip allowlist: %w", err)
	}
	return prefixes, nil
}

// Save writes prefixes as tenantID's allowlist; an empty list clears it.
func (s *Store) Save(ctx context.Context, tenantID string, prefixes []netip.Prefix) error {
	if err := s.config.Set(ctx, tenantID, Key, Format(prefixes)); err != nil {
		return fmt.Errorf("save ip allowlist: %w", err)
	}
	return nil
}

// Check is Load then Allows: whether tenantID's allowlist admits ip.
func (s *Store) Check(ctx context.Context, tenantID, ip string) (bool, error) {
	prefixes, err := s.Load(ctx, tenantID)
	if err != nil {
		return false, err
	}
	return Allows(prefixes, ip), nil
}
