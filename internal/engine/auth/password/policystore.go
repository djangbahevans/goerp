package password

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

// Tenant policy keys (auth-internals.md §3 "Password strength
// validation" and "Password policy at sign-in"). KeyChangedAt is written
// by tenantconfig.Store whenever KeyMinLength or KeyEnforcement changes
// value, and can't be written directly.
const (
	KeyMinLength   = tenantconfig.PasswordPolicyMinLengthKey
	KeyEnforcement = tenantconfig.PasswordPolicyEnforcementKey
	KeyGraceDays   = tenantconfig.PasswordPolicyGraceDaysKey
	KeyChangedAt   = tenantconfig.PasswordPolicyChangedAtKey
)

// Enforcement is how a tenant treats a member whose password falls short
// of its minimum.
type Enforcement string

const (
	// EnforcementNudge asks the member to change it and keeps full access.
	EnforcementNudge Enforcement = "nudge"
	// EnforcementRequire restricts the member's sessions after the grace
	// period until they change it.
	EnforcementRequire Enforcement = "require"
)

// Grace period bounds, in days.
const (
	DefaultGraceDays = 14
	MaxGraceDays     = 90
)

// TenantPolicy is a tenant's password settings, each already defaulted
// and range-checked.
type TenantPolicy struct {
	// MinLength is the tenant's own minimum: the larger of Global's and
	// its configured value.
	MinLength   int
	Enforcement Enforcement
	GraceDays   int
	// ChangedAt is when MinLength or Enforcement last changed value, nil
	// until either first does.
	ChangedAt *time.Time
}

// Members is satisfied by role.Store.
type Members interface {
	// CandidateTenantIDs lists the tenants the membership index has a
	// member row for userID in.
	CandidateTenantIDs(ctx context.Context, userID string) ([]string, error)
	// JoinedAt is when userID's member row in tenantSlug was created.
	JoinedAt(ctx context.Context, tenantSlug, userID string) (time.Time, error)
}

type PolicyStore struct {
	config  *tenantconfig.Store
	members Members
	now     func() time.Time
}

func NewPolicyStore(config *tenantconfig.Store, members Members) *PolicyStore {
	return &PolicyStore{config: config, members: members, now: time.Now}
}

// Tenant returns tenantID's password settings. A stored value that is
// out of range or unparseable is ignored with a warning rather than
// failing the password operation.
func (s *PolicyStore) Tenant(ctx context.Context, tenantID string) (TenantPolicy, error) {
	values, err := s.config.GetPrefix(ctx, tenantID, tenantconfig.PasswordPolicyPrefix)
	if err != nil {
		return TenantPolicy{}, fmt.Errorf("load password policy: %w", err)
	}

	p := TenantPolicy{MinLength: Global.MinLength, Enforcement: EnforcementNudge, GraceDays: DefaultGraceDays}
	if v, ok := values[KeyMinLength]; ok {
		if n, err := strconv.Atoi(v); err == nil && ValidateMinLength(n) == nil {
			p.MinLength = n
		} else {
			warnIgnored(tenantID, KeyMinLength, v)
		}
	}
	if v, ok := values[KeyEnforcement]; ok {
		if e := Enforcement(v); e == EnforcementNudge || e == EnforcementRequire {
			p.Enforcement = e
		} else {
			warnIgnored(tenantID, KeyEnforcement, v)
		}
	}
	if v, ok := values[KeyGraceDays]; ok {
		if n, err := strconv.Atoi(v); err == nil && ValidateGraceDays(n) == nil {
			p.GraceDays = n
		} else {
			warnIgnored(tenantID, KeyGraceDays, v)
		}
	}
	if v, ok := values[KeyChangedAt]; ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			p.ChangedAt = &t
		} else {
			warnIgnored(tenantID, KeyChangedAt, v)
		}
	}
	return p, nil
}

func warnIgnored(tenantID, key, value string) {
	log.Warn().Str("tenant_id", tenantID).Str("key", key).Str("value", value).Msg("password: invalid policy value, ignoring it")
}

// MinLength is tenantID's own minimum password length for a form to
// show, or Global's when tenantID is "" or its policy can't be read
// (logged).
func (s *PolicyStore) MinLength(ctx context.Context, tenantID string) int {
	if tenantID == "" {
		return Global.MinLength
	}
	p, err := s.Tenant(ctx, tenantID)
	if err != nil {
		log.Warn().Err(err).Str("tenant_id", tenantID).Msg("password: tenant min length lookup failed, reporting the global minimum")
		return Global.MinLength
	}
	return p.MinLength
}

// CombinedMinLength is the minimum a password being set for userID must
// meet: the largest own minimum among the tenants the account is a member
// of and joiningTenantID, the tenant the same request makes it a member
// of ("" for none). One password serves every tenant an account belongs
// to, so it has to satisfy all of them (auth-internals.md §3 "Password
// strength validation"). Tenants that require SSO would count only for
// their admins, but no tenant can require SSO yet.
func (s *PolicyStore) CombinedMinLength(ctx context.Context, userID, joiningTenantID string) (int, error) {
	var tenantIDs []string
	if userID != "" {
		ids, err := s.members.CandidateTenantIDs(ctx, userID)
		if err != nil {
			return 0, fmt.Errorf("list account tenants: %w", err)
		}
		tenantIDs = ids
	}
	if joiningTenantID != "" {
		tenantIDs = append(tenantIDs, joiningTenantID)
	}

	minLength := Global.MinLength
	for _, id := range tenantIDs {
		p, err := s.Tenant(ctx, id)
		if err != nil {
			return 0, err
		}
		minLength = max(minLength, p.MinLength)
	}
	return minLength, nil
}

// CheckSignIn checks plain, a password that just matched at a sign-in to
// tenantID, against that tenant's current rules (auth-internals.md §3
// "Password policy at sign-in"). A policy that can't be read is logged
// and reports nothing: the check never refuses a login.
func (s *PolicyStore) CheckSignIn(ctx context.Context, tenantID, tenantSlug, userID, plain, email string) Result {
	p, err := s.Tenant(ctx, tenantID)
	if err != nil {
		log.Warn().Err(err).Str("tenant_id", tenantID).Msg("password: sign-in policy check skipped")
		return Result{}
	}
	var joinedAt time.Time
	if p.Enforcement == EnforcementRequire {
		if joinedAt, err = s.members.JoinedAt(ctx, tenantSlug, userID); err != nil {
			log.Warn().Err(err).Str("tenant_id", tenantID).Str("user_id", userID).Msg("password: member join time lookup failed, applying the grace period")
			joinedAt = time.Time{}
		}
	}
	return p.CheckSignIn(plain, email, joinedAt, s.now())
}

// ErrInvalidSetting rejects a policy value PATCH /admin/settings can't
// store.
var ErrInvalidSetting = errors.New("invalid password policy setting")

// ValidateMinLength reports whether n is a tenant minimum Save can store.
func ValidateMinLength(n int) error {
	if n < Global.MinLength || n > TenantMaxMinLength {
		return fmt.Errorf("%w: min_length must be between %d and %d", ErrInvalidSetting, Global.MinLength, TenantMaxMinLength)
	}
	return nil
}

// ValidateGraceDays reports whether n is a grace period Save can store.
func ValidateGraceDays(n int) error {
	if n < 0 || n > MaxGraceDays {
		return fmt.Errorf("%w: grace_days must be between 0 and %d", ErrInvalidSetting, MaxGraceDays)
	}
	return nil
}

// ValidateEnforcement reports whether e is an enforcement Save can store.
func ValidateEnforcement(e Enforcement) error {
	if e != EnforcementNudge && e != EnforcementRequire {
		return fmt.Errorf("%w: enforcement must be %q or %q", ErrInvalidSetting, EnforcementNudge, EnforcementRequire)
	}
	return nil
}

// Save writes p's MinLength, Enforcement and GraceDays as tenantID's
// settings in one transaction; tenantconfig.Store stamps KeyChangedAt if
// MinLength or Enforcement changed. ChangedAt is ignored.
func (s *PolicyStore) Save(ctx context.Context, tenantID string, p TenantPolicy) error {
	if err := errors.Join(ValidateMinLength(p.MinLength), ValidateEnforcement(p.Enforcement), ValidateGraceDays(p.GraceDays)); err != nil {
		return err
	}
	if err := s.config.SetMany(ctx, tenantID, map[string]string{
		KeyMinLength:   strconv.Itoa(p.MinLength),
		KeyEnforcement: string(p.Enforcement),
		KeyGraceDays:   strconv.Itoa(p.GraceDays),
	}); err != nil {
		return fmt.Errorf("save password policy: %w", err)
	}
	return nil
}

// WriteTooWeak writes the 422 auth.password_too_weak error: err's
// message, with the minimum length that was applied as
// details.min_length.
func WriteTooWeak(ctx context.Context, w http.ResponseWriter, err error, minLength int) {
	httperr.WriteDetails(ctx, w, http.StatusUnprocessableEntity, "auth.password_too_weak", err.Error(), map[string]any{"min_length": minLength})
}
