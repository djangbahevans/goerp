package adminsettings

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/djangbahevans/goerp/internal/engine/auth/ipallowlist"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionpolicy"
	"github.com/djangbahevans/goerp/internal/engine/l10n"
	"github.com/djangbahevans/goerp/internal/engine/l10n/tenantl10n"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

const (
	maxNameLength       = 200
	maxAddressLength    = 500
	maxTaxIDLength      = 100
	maxWebsiteLength    = 2048
	maxAssuranceAgeDays = 30
)

var (
	countryPattern  = regexp.MustCompile(`^[A-Z]{2}$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
)

type Settings struct {
	General      General      `json:"general"`
	Security     Security     `json:"security"`
	Localisation Localisation `json:"localisation"`
}

// General is the company profile plus the tenant's default locale and
// timezone, which users who haven't set their own inherit.
type General struct {
	tenant.Profile  `json:",inline"`
	DefaultLocale   string `json:"default_locale"`
	DefaultTimezone string `json:"default_timezone"`
}

type Security struct {
	MFA            MFA            `json:"mfa"`
	PasswordPolicy PasswordPolicy `json:"password_policy"`
	Session        Session        `json:"session"`
	// IPAllowlist is comma-separated CIDR ranges; "" allows every address.
	IPAllowlist       string            `json:"ip_allowlist"`
	EmailVerification EmailVerification `json:"email_verification"`
}

const (
	VerificationRequired     = "required"
	VerificationTenantChoice = "tenant_choice"
	VerificationOff          = "off"
)

// requireEmailVerificationKey is the tenant's own choice, read only under
// the tenant_choice policy.
const requireEmailVerificationKey = "auth.require_email_verification"

// EmailVerification is the "require email verification on registration"
// setting. Policy is the platform's; Required is editable only under
// tenant_choice, and otherwise follows the policy.
type EmailVerification struct {
	Policy   string `json:"policy"`
	Required bool   `json:"required"`
}

type MFA struct {
	Mode                 string   `json:"mode"`
	RequiredRoles        []string `json:"required_roles"`
	MaxAssuranceAgeHours int      `json:"max_assurance_age_hours"`
}

// PasswordPolicy is the tenant's password settings (auth-internals.md §3
// "Password strength validation" and "Password policy at sign-in").
// ChangedAt is maintained by the engine and read-only.
type PasswordPolicy struct {
	MinLength   int        `json:"min_length"`
	Enforcement string     `json:"enforcement"`
	GraceDays   int        `json:"grace_days"`
	ChangedAt   *time.Time `json:"changed_at"`
}

// Session bounds are whole minutes; 0 is off.
type Session struct {
	IdleTimeoutMinutes int `json:"idle_timeout_minutes"`
	AbsoluteMaxMinutes int `json:"absolute_max_minutes"`
}

// Localisation's PlatformLocales is read-only: the locales the platform
// offers (GOERP_AVAILABLE_LOCALES), which AvailableLocales picks from.
type Localisation struct {
	PlatformLocales  []string `json:"platform_locales"`
	AvailableLocales []string `json:"available_locales"`
	FirstDayOfWeek   string   `json:"first_day_of_week"`
	NumberFormat     string   `json:"number_format"`
}

// patchRequest mirrors Settings with every field optional. An optional
// General text field is cleared by "".
type patchRequest struct {
	General      *generalPatch      `json:"general"`
	Security     *securityPatch     `json:"security"`
	Localisation *localisationPatch `json:"localisation"`
}

type generalPatch struct {
	Name            *string `json:"name"`
	Address         *string `json:"address"`
	TaxID           *string `json:"tax_id"`
	Website         *string `json:"website"`
	Country         *string `json:"country"`
	DefaultCurrency *string `json:"default_currency"`
	DefaultLocale   *string `json:"default_locale"`
	DefaultTimezone *string `json:"default_timezone"`
}

type securityPatch struct {
	MFA               *mfaPatch               `json:"mfa"`
	PasswordPolicy    *passwordPatch          `json:"password_policy"`
	Session           *sessionPatch           `json:"session"`
	IPAllowlist       *string                 `json:"ip_allowlist"`
	EmailVerification *emailVerificationPatch `json:"email_verification"`
}

type emailVerificationPatch struct {
	Required *bool `json:"required"`
}

type mfaPatch struct {
	Mode                 *string   `json:"mode"`
	RequiredRoles        *[]string `json:"required_roles"`
	MaxAssuranceAgeHours *int      `json:"max_assurance_age_hours"`
}

type passwordPatch struct {
	MinLength   *int    `json:"min_length"`
	Enforcement *string `json:"enforcement"`
	GraceDays   *int    `json:"grace_days"`
	// ChangedAt is decoded only to refuse it with read_only_key.
	ChangedAt jsontext.Value `json:"changed_at"`
}

type sessionPatch struct {
	IdleTimeoutMinutes *int `json:"idle_timeout_minutes"`
	AbsoluteMaxMinutes *int `json:"absolute_max_minutes"`
}

type localisationPatch struct {
	AvailableLocales *[]string `json:"available_locales"`
	FirstDayOfWeek   *string   `json:"first_day_of_week"`
	NumberFormat     *string   `json:"number_format"`
}

func (h *Handler) load(ctx context.Context, tc *tenantresolve.TenantContext) (*Settings, error) {
	profile, err := h.deps.TenantStore.GetProfile(ctx, tc.TenantID)
	if err != nil {
		return nil, fmt.Errorf("load profile: %w", err)
	}

	mfaPolicy, err := h.deps.MFA.LoadPolicy(ctx, tc.TenantID)
	if err != nil {
		return nil, err
	}
	requiredRoles := mfaPolicy.RequiredRoles
	if requiredRoles == nil {
		requiredRoles = []string{}
	}

	pw, err := h.deps.Passwords.Tenant(ctx, tc.TenantID)
	if err != nil {
		return nil, err
	}

	sessions, err := h.deps.Sessions.Load(ctx, tc.TenantID)
	if err != nil {
		return nil, err
	}

	// Shown as stored: Save writes the canonical form, and a value set
	// some other way that no longer parses stays visible to be fixed.
	allowlist, _, err := h.deps.Config.Get(ctx, tc.TenantID, ipallowlist.Key)
	if err != nil {
		return nil, fmt.Errorf("load ip allowlist: %w", err)
	}

	verification, err := h.emailVerification(ctx, tc.TenantID)
	if err != nil {
		return nil, err
	}

	l10n, err := h.deps.Locales.Load(ctx, tc.TenantID)
	if err != nil {
		return nil, err
	}

	return &Settings{
		General: General{
			Profile:         *profile,
			DefaultLocale:   l10n.DefaultLocale,
			DefaultTimezone: l10n.DefaultTimezone,
		},
		Security: Security{
			MFA: MFA{
				Mode:                 string(mfaPolicy.Mode),
				RequiredRoles:        requiredRoles,
				MaxAssuranceAgeHours: int(mfaPolicy.MaxAssuranceAge / time.Hour),
			},
			PasswordPolicy: PasswordPolicy{
				MinLength:   pw.MinLength,
				Enforcement: string(pw.Enforcement),
				GraceDays:   pw.GraceDays,
				ChangedAt:   pw.ChangedAt,
			},
			Session: Session{
				IdleTimeoutMinutes: int(sessions.IdleTimeout / time.Minute),
				AbsoluteMaxMinutes: int(sessions.AbsoluteMax / time.Minute),
			},
			IPAllowlist:       allowlist,
			EmailVerification: verification,
		},
		Localisation: Localisation{
			PlatformLocales:  h.deps.Locales.PlatformLocales(),
			AvailableLocales: l10n.AvailableLocales,
			FirstDayOfWeek:   l10n.FirstDayOfWeek,
			NumberFormat:     l10n.NumberFormat,
		},
	}, nil
}

// patchPlan is a validated PATCH: each non-nil write replaces that
// store's value, and changed names every field that differs from before.
type patchPlan struct {
	profile             *tenant.ProfileUpdate
	mfa                 *enforce.Policy
	password            *password.TenantPolicy
	session             *sessionpolicy.Policy
	allowlist           *[]netip.Prefix
	requireVerification *bool
	l10n                map[string]string
	changed             []string
}

// plan validates body against current, the settings it patches. A
// validation failure is a *fieldError; err is anything else.
func (h *Handler) plan(ctx context.Context, c caller, clientIP string, current *Settings, body patchRequest) (*patchPlan, *fieldError, error) {
	p := &patchPlan{}
	if g := body.General; g != nil {
		if ferr := p.planGeneral(current.General.Profile, g); ferr != nil {
			return nil, ferr, nil
		}
	}
	if s := body.Security; s != nil {
		if s.MFA != nil {
			ferr, err := h.planMFA(ctx, c, p, current.Security.MFA, s.MFA)
			if ferr != nil || err != nil {
				return nil, ferr, err
			}
		}
		if s.PasswordPolicy != nil {
			if ferr := p.planPassword(current.Security.PasswordPolicy, s.PasswordPolicy); ferr != nil {
				return nil, ferr, nil
			}
		}
		if s.Session != nil {
			if ferr := p.planSession(current.Security.Session, s.Session); ferr != nil {
				return nil, ferr, nil
			}
		}
		if s.IPAllowlist != nil {
			if ferr := p.planAllowlist(current.Security.IPAllowlist, *s.IPAllowlist, clientIP); ferr != nil {
				return nil, ferr, nil
			}
		}
		if s.EmailVerification != nil && s.EmailVerification.Required != nil {
			if ferr := p.planEmailVerification(current.Security.EmailVerification, *s.EmailVerification.Required); ferr != nil {
				return nil, ferr, nil
			}
		}
	}
	if ferr := h.planLocalisation(p, current, body.General, body.Localisation); ferr != nil {
		return nil, ferr, nil
	}
	return p, nil, nil
}

func (p *patchPlan) planGeneral(current tenant.Profile, g *generalPatch) *fieldError {
	update := tenant.ProfileUpdate{}
	changed := false
	set := func(field string, dst **string, value string, was *string) {
		*dst = new(value)
		if (was == nil && value != "") || (was != nil && *was != value) {
			p.changed = append(p.changed, "general."+field)
			changed = true
		}
	}

	if g.Name != nil {
		name := strings.TrimSpace(*g.Name)
		if name == "" || utf8.RuneCountInString(name) > maxNameLength {
			return invalid("general.name", "a company name is 1 to %d characters", maxNameLength)
		}
		set("name", &update.Name, name, &current.Name)
	}
	if g.Address != nil {
		address := strings.TrimSpace(*g.Address)
		if utf8.RuneCountInString(address) > maxAddressLength {
			return invalid("general.address", "an address is at most %d characters", maxAddressLength)
		}
		set("address", &update.Address, address, current.Address)
	}
	if g.TaxID != nil {
		taxID := strings.TrimSpace(*g.TaxID)
		if utf8.RuneCountInString(taxID) > maxTaxIDLength {
			return invalid("general.tax_id", "a tax ID is at most %d characters", maxTaxIDLength)
		}

		set("tax_id", &update.TaxID, taxID, current.TaxID)
	}
	if g.Website != nil {
		website := strings.TrimSpace(*g.Website)
		if website != "" && !validWebsite(website) {
			return invalid("general.website", "a website is an http or https URL")
		}
		set("website", &update.Website, website, current.Website)
	}
	if g.Country != nil {
		country := strings.ToUpper(strings.TrimSpace(*g.Country))
		if country != "" && !countryPattern.MatchString(country) {
			return invalid("general.country", "a country is a two-letter ISO 3166-1 code, such as GH")
		}
		set("country", &update.Country, country, current.Country)
	}
	if g.DefaultCurrency != nil {
		currency := strings.ToUpper(strings.TrimSpace(*g.DefaultCurrency))
		if currency != "" && !currencyPattern.MatchString(currency) {
			return invalid("general.default_currency", "a currency is a three-letter ISO 4217 code, such as GHS")
		}
		set("default_currency", &update.DefaultCurrency, currency, current.DefaultCurrency)
	}
	if changed {
		p.profile = &update
	}
	return nil
}

func validWebsite(s string) bool {
	if len(s) > maxWebsiteLength {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

func (h *Handler) planMFA(ctx context.Context, c caller, p *patchPlan, current MFA, m *mfaPatch) (*fieldError, error) {
	next := current
	if m.Mode != nil {
		next.Mode = *m.Mode
	}
	if m.RequiredRoles != nil {
		next.RequiredRoles = []string{}
		for _, name := range *m.RequiredRoles {
			if name = strings.TrimSpace(name); name != "" && !slices.Contains(next.RequiredRoles, name) {
				next.RequiredRoles = append(next.RequiredRoles, name)
			}
		}
	} else if enforce.Mode(next.Mode) != enforce.ModeRequiredForRoles {
		// Leaving required_for_roles drops its roles, which no other
		// mode reads, rather than rejecting a request that only changes
		// the mode.
		next.RequiredRoles = []string{}
	}
	if m.MaxAssuranceAgeHours != nil {
		next.MaxAssuranceAgeHours = *m.MaxAssuranceAgeHours
	}

	if !enforce.ValidMode(enforce.Mode(next.Mode)) {
		return invalid("security.mfa.mode", "mode is one of optional, required, required_for_roles"), nil
	}
	if next.MaxAssuranceAgeHours < 1 || next.MaxAssuranceAgeHours > maxAssuranceAgeDays*24 {
		return invalid("security.mfa.max_assurance_age_hours", "max assurance age is 1 to %d hours", maxAssuranceAgeDays*24), nil
	}
	if enforce.Mode(next.Mode) == enforce.ModeRequiredForRoles {
		if len(next.RequiredRoles) == 0 {
			return invalid("security.mfa.required_roles", "required_for_roles needs at least one role"), nil
		}
		// Only newly added roles must exist: a stored role deleted since
		// shouldn't block every later MFA edit that resends the list.
		added := slices.DeleteFunc(slices.Clone(next.RequiredRoles), func(name string) bool {
			return slices.Contains(current.RequiredRoles, name)
		})
		if len(added) > 0 {
			roles, err := h.deps.Roles.AllRoles(ctx, c.tenant.Slug)
			if err != nil {
				return nil, fmt.Errorf("list roles: %w", err)
			}
			for _, name := range added {
				if !slices.ContainsFunc(roles, func(r role.Role) bool { return r.Name == name }) {
					return invalid("security.mfa.required_roles", "unknown role %q", name), nil
				}
			}
		}
	} else if len(next.RequiredRoles) > 0 {
		return invalid("security.mfa.required_roles", "required roles apply only to the required_for_roles mode"), nil
	}

	if next.Mode == current.Mode && slices.Equal(next.RequiredRoles, current.RequiredRoles) && next.MaxAssuranceAgeHours == current.MaxAssuranceAgeHours {
		return nil, nil
	}
	p.mfa = &enforce.Policy{
		Mode:            enforce.Mode(next.Mode),
		RequiredRoles:   next.RequiredRoles,
		MaxAssuranceAge: time.Duration(next.MaxAssuranceAgeHours) * time.Hour,
	}
	p.changed = append(p.changed, "security.mfa")
	return nil, nil
}

func (p *patchPlan) planPassword(current PasswordPolicy, pp *passwordPatch) *fieldError {
	next := password.TenantPolicy{MinLength: current.MinLength, Enforcement: password.Enforcement(current.Enforcement), GraceDays: current.GraceDays}
	setInt(&next.MinLength, pp.MinLength)
	if pp.Enforcement != nil {
		next.Enforcement = password.Enforcement(*pp.Enforcement)
	}
	setInt(&next.GraceDays, pp.GraceDays)

	if err := password.ValidateMinLength(next.MinLength); err != nil {
		return invalid("security.password_policy.min_length", "%s", err.Error())
	}
	if err := password.ValidateEnforcement(next.Enforcement); err != nil {
		return invalid("security.password_policy.enforcement", "%s", err.Error())
	}
	if err := password.ValidateGraceDays(next.GraceDays); err != nil {
		return invalid("security.password_policy.grace_days", "%s", err.Error())
	}
	if next.MinLength == current.MinLength && string(next.Enforcement) == current.Enforcement && next.GraceDays == current.GraceDays {
		return nil
	}
	p.password = &next
	p.changed = append(p.changed, "security.password_policy")
	return nil
}

func (p *patchPlan) planSession(current Session, s *sessionPatch) *fieldError {
	next := current
	setInt(&next.IdleTimeoutMinutes, s.IdleTimeoutMinutes)
	setInt(&next.AbsoluteMaxMinutes, s.AbsoluteMaxMinutes)

	if next.IdleTimeoutMinutes < 0 || next.AbsoluteMaxMinutes < 0 {
		return invalid("security.session", "session bounds are whole minutes, 0 for none")
	}
	policy := sessionpolicy.Policy{
		IdleTimeout: time.Duration(next.IdleTimeoutMinutes) * time.Minute,
		AbsoluteMax: time.Duration(next.AbsoluteMaxMinutes) * time.Minute,
	}
	if err := policy.Validate(); err != nil {
		return invalid("security.session", "%s", strings.TrimPrefix(err.Error(), sessionpolicy.ErrInvalid.Error()+": "))
	}
	if next == current {
		return nil
	}
	p.session = &policy
	p.changed = append(p.changed, "security.session")
	return nil
}

// planAllowlist refuses a changed list that leaves out clientIP, which
// would stop the admin saving it from signing in again. An unchanged list
// is accepted as is, so a form that resends it can still save other
// fields from an address the list no longer covers.
func (p *patchPlan) planAllowlist(current, value, clientIP string) *fieldError {
	prefixes, err := ipallowlist.Parse(value)
	if err != nil {
		return invalid("security.ip_allowlist", "%s", err.Error())
	}
	if ipallowlist.Format(prefixes) == current {
		return nil
	}
	if !ipallowlist.Allows(prefixes, clientIP) {
		return invalid("security.ip_allowlist", "the allowlist must include the address you are signed in from (%s)", clientIP)
	}
	p.allowlist = &prefixes
	p.changed = append(p.changed, "security.ip_allowlist")
	return nil
}

// planEmailVerification accepts a change only under the tenant_choice
// policy; the other policies fix the value, so resending it is a no-op.
func (p *patchPlan) planEmailVerification(current EmailVerification, required bool) *fieldError {
	if required == current.Required {
		return nil
	}
	if current.Policy != VerificationTenantChoice {
		return invalid("security.email_verification", "email verification is set by your platform configuration")
	}
	p.requireVerification = &required
	p.changed = append(p.changed, "security.email_verification")
	return nil
}

// emailVerification resolves the setting as the page shows it. Under
// tenant_choice an unset tenant value is true, the same outcome
// registration uses for a tenant with no choice of its own.
func (h *Handler) emailVerification(ctx context.Context, tenantID string) (EmailVerification, error) {
	policy := cmp.Or(h.deps.EmailVerificationPolicy, VerificationTenantChoice)
	switch policy {
	case VerificationRequired:
		return EmailVerification{Policy: policy, Required: true}, nil
	case VerificationOff:
		return EmailVerification{Policy: policy, Required: false}, nil
	}
	raw, ok, err := h.deps.Config.Get(ctx, tenantID, requireEmailVerificationKey)
	if err != nil {
		return EmailVerification{}, fmt.Errorf("load email verification setting: %w", err)
	}
	required := true
	if ok {
		if required, err = strconv.ParseBool(raw); err != nil {
			return EmailVerification{}, fmt.Errorf("parse %s %q: %w", requireEmailVerificationKey, raw, err)
		}
	}
	return EmailVerification{Policy: policy, Required: required}, nil
}

// planLocalisation plans every tenantl10n key: Localisation's fields and
// General's default locale and timezone. The default locale must stay one
// of the available locales, whichever of the two a request changes.
func (h *Handler) planLocalisation(p *patchPlan, current *Settings, g *generalPatch, l *localisationPatch) *fieldError {
	values := map[string]string{}
	platform := h.deps.Locales.PlatformLocales()

	available := current.Localisation.AvailableLocales
	if l != nil && l.AvailableLocales != nil {
		var locales []string
		for _, locale := range *l.AvailableLocales {
			if !slices.Contains(platform, locale) {
				return invalid("localisation.available_locales", "%q is not a locale this platform offers (%s)", locale, strings.Join(platform, ", "))
			}
			if !slices.Contains(locales, locale) {
				locales = append(locales, locale)
			}
		}
		if len(locales) == 0 {
			return invalid("localisation.available_locales", "at least one locale must be available")
		}
		if !slices.Equal(locales, available) {
			values[tenantl10n.KeyAvailableLocales] = strings.Join(locales, ",")
			p.changed = append(p.changed, "localisation.available_locales")
		}
		available = locales
	}

	if g != nil && g.DefaultLocale != nil {
		if !slices.Contains(available, *g.DefaultLocale) {
			return invalid("general.default_locale", "the default locale is one of the available locales (%s)", strings.Join(available, ", "))
		}
		if *g.DefaultLocale != current.General.DefaultLocale {
			values[tenantl10n.KeyDefaultLocale] = *g.DefaultLocale
			p.changed = append(p.changed, "general.default_locale")
		}
	} else if !slices.Contains(available, current.General.DefaultLocale) {
		return invalid("localisation.available_locales", "the available locales must include the default locale %q", current.General.DefaultLocale)
	}
	if g != nil && g.DefaultTimezone != nil {
		if !l10n.ValidTimezone(*g.DefaultTimezone) {
			return invalid("general.default_timezone", "the default timezone is an IANA time zone name, such as Africa/Accra")
		}
		if *g.DefaultTimezone != current.General.DefaultTimezone {
			values[tenantl10n.KeyDefaultTimezone] = *g.DefaultTimezone
			p.changed = append(p.changed, "general.default_timezone")
		}
	}

	if l != nil && l.FirstDayOfWeek != nil {
		if !slices.Contains(tenantl10n.FirstDaysOfWeek, *l.FirstDayOfWeek) {
			return invalid("localisation.first_day_of_week", "first day of week is one of %s", strings.Join(tenantl10n.FirstDaysOfWeek, ", "))
		}
		if *l.FirstDayOfWeek != current.Localisation.FirstDayOfWeek {
			values[tenantl10n.KeyFirstDayOfWeek] = *l.FirstDayOfWeek
			p.changed = append(p.changed, "localisation.first_day_of_week")
		}
	}
	if l != nil && l.NumberFormat != nil {
		if !slices.Contains(tenantl10n.NumberFormats, *l.NumberFormat) {
			return invalid("localisation.number_format", "number format is one of %s", strings.Join(tenantl10n.NumberFormats, " | "))
		}
		if *l.NumberFormat != current.Localisation.NumberFormat {
			values[tenantl10n.KeyNumberFormat] = *l.NumberFormat
			p.changed = append(p.changed, "localisation.number_format")
		}
	}
	if len(values) > 0 {
		p.l10n = values
	}
	return nil
}

// apply writes a validated plan, stopping at the first failure. Each
// store commits on its own, so a failure partway leaves the earlier
// stores written: committed names their changed fields either way, for
// the caller to audit.
func (h *Handler) apply(ctx context.Context, tc *tenantresolve.TenantContext, p *patchPlan) (committed []string, err error) {
	isLocale := func(f string) bool {
		return strings.HasPrefix(f, "localisation.") || f == "general.default_locale" || f == "general.default_timezone"
	}
	steps := []struct {
		pending bool
		write   func() error
		owns    func(field string) bool
	}{
		{p.profile != nil, func() error {
			if _, err := h.deps.TenantStore.UpdateProfile(ctx, tc.TenantID, *p.profile); err != nil {
				return fmt.Errorf("save profile: %w", err)
			}
			return nil
		}, func(f string) bool { return strings.HasPrefix(f, "general.") && !isLocale(f) }},
		{p.mfa != nil, func() error { return h.deps.MFA.SavePolicy(ctx, tc.TenantID, *p.mfa) },
			func(f string) bool { return f == "security.mfa" }},
		{p.password != nil, func() error { return h.deps.Passwords.Save(ctx, tc.TenantID, *p.password) },
			func(f string) bool { return f == "security.password_policy" }},
		{p.session != nil, func() error { return h.deps.Sessions.Save(ctx, tc.TenantID, *p.session) },
			func(f string) bool { return f == "security.session" }},
		{p.allowlist != nil, func() error { return h.deps.IPAllowlists.Save(ctx, tc.TenantID, *p.allowlist) },
			func(f string) bool { return f == "security.ip_allowlist" }},
		{p.requireVerification != nil, func() error {
			if err := h.deps.Config.Set(ctx, tc.TenantID, requireEmailVerificationKey, strconv.FormatBool(*p.requireVerification)); err != nil {
				return fmt.Errorf("save email verification setting: %w", err)
			}
			return nil
		}, func(f string) bool { return f == "security.email_verification" }},
		{p.l10n != nil, func() error {
			if err := h.deps.Config.SetMany(ctx, tc.TenantID, p.l10n); err != nil {
				return fmt.Errorf("save localisation settings: %w", err)
			}
			return nil
		}, isLocale},
	}
	for _, step := range steps {
		if !step.pending {
			continue
		}
		if err := step.write(); err != nil {
			return committed, err
		}
		for _, f := range p.changed {
			if step.owns(f) {
				committed = append(committed, f)
			}
		}
	}
	return committed, nil
}

func setInt(dst *int, v *int) {
	if v != nil {
		*dst = *v
	}
}
