package password

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

// Tenant policy keys, each under tenantconfig.PasswordPolicyPrefix. A
// write to any of them bumps tenantconfig.PasswordPolicyVersionKey.
const (
	KeyMinLength        = tenantconfig.PasswordPolicyPrefix + "min_length"
	KeyMaxLength        = tenantconfig.PasswordPolicyPrefix + "max_length"
	KeyRequireUppercase = tenantconfig.PasswordPolicyPrefix + "require_uppercase"
	KeyRequireDigit     = tenantconfig.PasswordPolicyPrefix + "require_digit"
	KeyRequireSymbol    = tenantconfig.PasswordPolicyPrefix + "require_symbol"
	KeyBlockCommonList  = tenantconfig.PasswordPolicyPrefix + "block_common_list"
	KeyBlockUserInfo    = tenantconfig.PasswordPolicyPrefix + "block_user_info"
)

type PolicyStore struct {
	config *tenantconfig.Store
}

func NewPolicyStore(config *tenantconfig.Store) *PolicyStore {
	return &PolicyStore{config: config}
}

// Effective returns the strictest of Global and tenantID's policy, and
// the tenant's current policy version (0 until its policy first
// changes). An unparseable tenant value is ignored with a warning rather
// than failing the password operation.
func (s *PolicyStore) Effective(ctx context.Context, tenantID string) (Policy, int64, error) {
	values, err := s.config.GetPrefix(ctx, tenantID, "auth.password_policy")
	if err != nil {
		return Policy{}, 0, fmt.Errorf("load password policy: %w", err)
	}

	tenant := Policy{MaxLength: Global.MaxLength}
	parseInt(tenantID, values, KeyMinLength, &tenant.MinLength)
	parseInt(tenantID, values, KeyMaxLength, &tenant.MaxLength)
	parseBool(tenantID, values, KeyRequireUppercase, &tenant.RequireUppercase)
	parseBool(tenantID, values, KeyRequireDigit, &tenant.RequireDigit)
	parseBool(tenantID, values, KeyRequireSymbol, &tenant.RequireSymbol)
	parseBool(tenantID, values, KeyBlockCommonList, &tenant.BlockCommonList)
	parseBool(tenantID, values, KeyBlockUserInfo, &tenant.BlockUserInfo)

	// Either bound out of range would reject every password.
	if tenant.MinLength > Global.MaxLength {
		log.Warn().Str("tenant_id", tenantID).Int("min_length", tenant.MinLength).Msg("password: tenant min length above the global max length, ignoring it")
		tenant.MinLength = 0
	}
	effective := Global.Strictest(tenant)
	if effective.MaxLength < effective.MinLength {
		log.Warn().Str("tenant_id", tenantID).Int("max_length", tenant.MaxLength).Msg("password: tenant max length below min length, ignoring it")
		effective.MaxLength = Global.MaxLength
	}

	var version int64
	if v, ok := values[tenantconfig.PasswordPolicyVersionKey]; ok {
		version, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			return Policy{}, 0, fmt.Errorf("parse password policy version %q: %w", v, err)
		}
	}

	return effective, version, nil
}

// MinLength is the effective minimum password length for a form to show:
// Effective's for tenantID, or Global's when tenantID is "" or its policy
// can't be read (logged).
func (s *PolicyStore) MinLength(ctx context.Context, tenantID string) int {
	if tenantID == "" {
		return Global.MinLength
	}
	policy, _, err := s.Effective(ctx, tenantID)
	if err != nil {
		log.Warn().Err(err).Str("tenant_id", tenantID).Msg("password: effective min length lookup failed, reporting the global minimum")
		return Global.MinLength
	}
	return policy.MinLength
}

// WriteTooWeak writes the 422 auth.password_too_weak error: err's
// message, with the minimum length p enforces as details.min_length.
func WriteTooWeak(ctx context.Context, w http.ResponseWriter, err error, p Policy) {
	httperr.WriteDetails(ctx, w, http.StatusUnprocessableEntity, "auth.password_too_weak", err.Error(), map[string]any{"min_length": p.MinLength})
}

func parseInt(tenantID string, values map[string]string, key string, dst *int) {
	v, ok := values[key]
	if !ok {
		return
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Warn().Str("tenant_id", tenantID).Str("key", key).Str("value", v).Msg("password: invalid policy value, ignoring it")
		return
	}
	*dst = n
}

func parseBool(tenantID string, values map[string]string, key string, dst *bool) {
	v, ok := values[key]
	if !ok {
		return
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Warn().Str("tenant_id", tenantID).Str("key", key).Str("value", v).Msg("password: invalid policy value, ignoring it")
		return
	}
	*dst = b
}

// UpdateRecommended reports whether a login into tenantID, currently at
// policy version currentVersion, should carry the password-update nudge.
// A password validated against another tenant's policy (or only the
// global one) was never checked against this tenant's, so it's behind
// once this tenant has changed its policy at all.
func UpdateRecommended(setTenantID *string, setVersion int64, tenantID string, currentVersion int64) bool {
	if currentVersion == 0 {
		return false
	}
	if setTenantID == nil || *setTenantID != tenantID {
		return true
	}
	return setVersion < currentVersion
}

// ErrLooserThanGlobal rejects a tenant policy that would loosen Global:
// Effective keeps the stricter value field by field, so a looser one
// would be stored but never applied.
var ErrLooserThanGlobal = errors.New("password policy cannot be looser than the platform policy")

// ValidateTenant reports whether p is a tenant policy Save can store:
// every field at least as strict as Global, and a length range that
// admits some password.
func ValidateTenant(p Policy) error {
	switch {
	case p.MinLength < Global.MinLength:
		return fmt.Errorf("%w: min_length must be at least %d", ErrLooserThanGlobal, Global.MinLength)
	case p.MaxLength > Global.MaxLength:
		return fmt.Errorf("%w: max_length must be at most %d", ErrLooserThanGlobal, Global.MaxLength)
	case p.MaxLength < p.MinLength:
		return fmt.Errorf("%w: max_length must be at least min_length", ErrLooserThanGlobal)
	case Global.RequireUppercase && !p.RequireUppercase,
		Global.RequireDigit && !p.RequireDigit,
		Global.RequireSymbol && !p.RequireSymbol,
		Global.BlockCommonList && !p.BlockCommonList,
		Global.BlockUserInfo && !p.BlockUserInfo:
		return fmt.Errorf("%w: a rule the platform requires cannot be turned off", ErrLooserThanGlobal)
	}
	return nil
}

// Save writes p as tenantID's policy in one transaction, bumping the
// policy version once if any field changed. p must pass ValidateTenant.
func (s *PolicyStore) Save(ctx context.Context, tenantID string, p Policy) error {
	if err := ValidateTenant(p); err != nil {
		return err
	}
	if err := s.config.SetMany(ctx, tenantID, map[string]string{
		KeyMinLength:        strconv.Itoa(p.MinLength),
		KeyMaxLength:        strconv.Itoa(p.MaxLength),
		KeyRequireUppercase: strconv.FormatBool(p.RequireUppercase),
		KeyRequireDigit:     strconv.FormatBool(p.RequireDigit),
		KeyRequireSymbol:    strconv.FormatBool(p.RequireSymbol),
		KeyBlockCommonList:  strconv.FormatBool(p.BlockCommonList),
		KeyBlockUserInfo:    strconv.FormatBool(p.BlockUserInfo),
	}); err != nil {
		return fmt.Errorf("save password policy: %w", err)
	}
	return nil
}
