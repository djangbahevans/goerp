// Package notifconfig is the tenant-level notification configuration
// (notification-system.md §13): the platform-wide per-channel kill
// switches, the email provider section, the SMS sender ID,
// and per-notification-type tenant default channels. Every key lives in
// module_config under the reserved "engine" namespace and resolves
// through tenantconfig.Resolver like any module's own config.
package notifconfig

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// Namespace is the module_config module_name every key below is stored
// under; a key's fully namespaced form is Namespace + "." + key.
const Namespace = manifest.ReservedEngineName

const (
	KeyEmailEnabled = "notifications.email_enabled"
	KeySMSEnabled   = "notifications.sms_enabled"
	KeyPushEnabled  = "notifications.push_enabled"

	KeyEmailProvider = "notifications.email.provider"
	KeyEmailAPIKey   = "notifications.email.api_key"
	KeyEmailFromName = "notifications.email.from_name"
	KeyEmailFromAddr = "notifications.email.from_addr"
	KeyEmailReplyTo  = "notifications.email.reply_to"
	KeyEmailLayout   = "notifications.email.layout_template"

	KeySMTPHost     = "notifications.email.smtp.host"
	KeySMTPPort     = "notifications.email.smtp.port"
	KeySMTPUser     = "notifications.email.smtp.user"
	KeySMTPPassword = "notifications.email.smtp.password"
	KeySMTPUseTLS   = "notifications.email.smtp.use_tls"

	KeySMSSenderID = "notifications.sms.sender_id"

	KeyDefaults = "notification_defaults"
)

const (
	ProviderResend = "resend"
	ProviderSMTP   = "smtp"
)

var (
	ErrUnknownKey   = errors.New("unknown notification config key")
	ErrInvalidValue = errors.New("invalid notification config value")
	ErrNoRowKeys    = errors.New("no row encryption key is configured")
)

// Schema declares every key's type, default and encryption, in the same
// shape a module's own config_schema uses.
var Schema = []manifest.ConfigEntry{
	{Key: KeyEmailEnabled, Type: "boolean", Default: true},
	{Key: KeySMSEnabled, Type: "boolean", Default: true},
	{Key: KeyPushEnabled, Type: "boolean", Default: true},

	{Key: KeyEmailProvider, Type: "string", Default: ProviderResend, Options: []manifest.FieldOption{{Value: ProviderResend}, {Value: ProviderSMTP}}},
	{Key: KeyEmailAPIKey, Type: "string", Encrypted: true},
	{Key: KeyEmailFromName, Type: "string"},
	{Key: KeyEmailFromAddr, Type: "string"},
	{Key: KeyEmailReplyTo, Type: "string"},
	{Key: KeyEmailLayout, Type: "string"},

	{Key: KeySMTPHost, Type: "string"},
	{Key: KeySMTPPort, Type: "integer", Default: 587},
	{Key: KeySMTPUser, Type: "string"},
	{Key: KeySMTPPassword, Type: "string", Encrypted: true},
	{Key: KeySMTPUseTLS, Type: "boolean", Default: true},

	{Key: KeySMSSenderID, Type: "string"},

	{Key: KeyDefaults, Type: "json"},
}

func entryFor(key string) (manifest.ConfigEntry, bool) {
	i := slices.IndexFunc(Schema, func(e manifest.ConfigEntry) bool { return e.Key == key })
	if i < 0 {
		return manifest.ConfigEntry{}, false
	}
	return Schema[i], true
}

// Seeds are the values tenant provisioning writes into module_config.
// Only the kill switches are seeded; every other key stays unset until
// a tenant admin configures it.
func Seeds() map[string]any {
	return map[string]any{
		KeyEmailEnabled: true,
		KeySMSEnabled:   true,
		KeyPushEnabled:  true,
	}
}

type Config struct {
	EmailEnabled bool
	SMSEnabled   bool
	PushEnabled  bool

	Email EmailConfig
	SMS   SMSConfig

	// Defaults maps a notification type to the channels it routes to for
	// a user with no preference of their own, overriding the type's
	// manifest default_channels.
	Defaults map[string][]string
}

type EmailConfig struct {
	Provider string
	APIKey   string
	FromName string
	FromAddr string
	ReplyTo  string
	// LayoutTemplate, when set, replaces the default email layout:
	// "{theme_module}/{path}", a file inside an installed theme module's
	// package.
	LayoutTemplate string
	SMTP           SMTPConfig
}

type SMTPConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	UseTLS   bool
}

// SMSConfig holds engine-level SMS settings the engine passes to the
// selected connector in the sms_send payload; the connector itself is
// chosen by tenant_provider_selections.
type SMSConfig struct {
	SenderID string
}

// DefaultChannels is routing step 2: the tenant's notification_defaults
// entry for notificationType, or manifestDefault when there is none.
func (c *Config) DefaultChannels(notificationType string, manifestDefault []string) []string {
	if channels, ok := c.Defaults[notificationType]; ok {
		return slices.Clone(channels)
	}
	return slices.Clone(manifestDefault)
}

// ChannelEnabled reports whether the tenant's kill switch allows channel.
// in_app has no kill switch.
func (c *Config) ChannelEnabled(channel string) bool {
	switch channel {
	case notifications.ChannelEmail:
		return c.EmailEnabled
	case notifications.ChannelSMS:
		return c.SMSEnabled
	case notifications.ChannelPush:
		return c.PushEnabled
	default:
		return true
	}
}

// ApplyKillSwitches is routing step 8: it removes every channel the
// tenant has disabled, whatever put it in channels.
func (c *Config) ApplyKillSwitches(channels []string) []string {
	return slices.DeleteFunc(slices.Clone(channels), func(ch string) bool { return !c.ChannelEnabled(ch) })
}

type Service struct {
	resolver *tenantconfig.Resolver
	store    *tenantconfig.Store
	rowKeys  *rowcrypt.RowKeySet
}

func NewService(resolver *tenantconfig.Resolver, store *tenantconfig.Store, rowKeys *rowcrypt.RowKeySet) *Service {
	return &Service{resolver: resolver, store: store, rowKeys: rowKeys}
}

// Load resolves tenantID's full notification configuration, falling back
// to Schema's defaults for any key no tier sets.
func (s *Service) Load(ctx context.Context, tenantID string) (*Config, error) {
	values := make(map[string]any, len(Schema))
	for _, entry := range Schema {
		v, err := s.get(ctx, tenantID, entry)
		if err != nil {
			return nil, err
		}
		values[entry.Key] = v
	}

	str := func(k string) string { s, _ := values[k].(string); return s }
	boolean := func(k string) bool { b, _ := values[k].(bool); return b }
	port, _ := values[KeySMTPPort].(int)

	defaults, err := decodeDefaults(values[KeyDefaults])
	if err != nil {
		return nil, err
	}

	return &Config{
		EmailEnabled: boolean(KeyEmailEnabled),
		SMSEnabled:   boolean(KeySMSEnabled),
		PushEnabled:  boolean(KeyPushEnabled),
		Email: EmailConfig{
			Provider:       str(KeyEmailProvider),
			APIKey:         str(KeyEmailAPIKey),
			FromName:       str(KeyEmailFromName),
			FromAddr:       str(KeyEmailFromAddr),
			ReplyTo:        str(KeyEmailReplyTo),
			LayoutTemplate: str(KeyEmailLayout),
			SMTP: SMTPConfig{
				Host:     str(KeySMTPHost),
				Port:     port,
				User:     str(KeySMTPUser),
				Password: str(KeySMTPPassword),
				UseTLS:   boolean(KeySMTPUseTLS),
			},
		},
		SMS:      SMSConfig{SenderID: str(KeySMSSenderID)},
		Defaults: defaults,
	}, nil
}

func (s *Service) get(ctx context.Context, tenantID string, entry manifest.ConfigEntry) (any, error) {
	raw, encrypted, found, err := s.resolver.Get(ctx, tenantID, Namespace+"."+entry.Key)
	if err != nil {
		return nil, err
	}
	if !found {
		return entry.Default, nil
	}

	if encrypted {
		if raw, err = s.decrypt(raw); err != nil {
			return nil, fmt.Errorf("decrypt %s: %w", entry.Key, err)
		}
	}

	switch entry.Type {
	case "boolean":
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidValue, entry.Key, err)
		}
		return v, nil
	case "integer":
		v, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidValue, entry.Key, err)
		}
		return v, nil
	default:
		return raw, nil
	}
}

func (s *Service) decrypt(raw string) (string, error) {
	if s.rowKeys == nil {
		return "", ErrNoRowKeys
	}
	plaintext, err := s.rowKeys.Decrypt([]byte(raw))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func decodeDefaults(v any) (map[string][]string, error) {
	raw, _ := v.(string)
	if raw == "" {
		return map[string][]string{}, nil
	}
	var defaults map[string][]string
	if err := json.Unmarshal([]byte(raw), &defaults); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalidValue, KeyDefaults, err)
	}
	return defaults, nil
}

// Set writes key's tenant-admin value for the tenant, encrypting an
// "encrypted" key before it reaches module_config. updatedBy may be empty.
func (s *Service) Set(ctx context.Context, tenantID, tenantSlug, key string, value any, updatedBy string) error {
	return s.SetMany(ctx, tenantID, tenantSlug, map[string]any{key: value}, updatedBy)
}

// SetMany is Set for several keys in one transaction: every value is
// validated before anything is written, and every write lands or none
// does. A nil value removes the tenant's value, so the key falls back to
// its default.
func (s *Service) SetMany(ctx context.Context, tenantID, tenantSlug string, values map[string]any, updatedBy string) error {
	writes := make([]tenantconfig.ModuleConfigValue, 0, len(values))
	for key, value := range values {
		entry, ok := entryFor(key)
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownKey, key)
		}
		if value == nil {
			writes = append(writes, tenantconfig.ModuleConfigValue{Key: key})
			continue
		}
		data, err := s.encode(entry, value)
		if err != nil {
			return err
		}
		writes = append(writes, tenantconfig.ModuleConfigValue{Key: key, Value: data, Type: entry.Type, Encrypted: entry.Encrypted})
	}

	if err := s.store.SetModuleConfigMany(ctx, tenantID, tenantschema.Name(tenantSlug), Namespace, writes, updatedBy); err != nil {
		return err
	}
	for key := range values {
		s.resolver.Invalidate(tenantID, Namespace+"."+key)
	}
	return nil
}

// encode validates value and renders it as module_config's JSONB value.
func (s *Service) encode(entry manifest.ConfigEntry, value any) ([]byte, error) {
	if err := validate(entry, value); err != nil {
		return nil, err
	}

	if entry.Encrypted {
		if s.rowKeys == nil {
			return nil, ErrNoRowKeys
		}
		ciphertext, err := s.rowKeys.Encrypt([]byte(value.(string)))
		if err != nil {
			return nil, fmt.Errorf("encrypt %s: %w", entry.Key, err)
		}
		value = string(ciphertext)
	}

	if n, ok := asInt(value); ok && entry.Type == "integer" {
		value = n
	}

	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", entry.Key, err)
	}
	return data, nil
}

// Locked returns the keys an operator override in
// tenant_config_overrides fixes for the tenant, read uncached.
func (s *Service) Locked(ctx context.Context, tenantID string) ([]string, error) {
	overridden, err := s.store.GetPrefix(ctx, tenantID, Namespace+".")
	if err != nil {
		return nil, err
	}
	var locked []string
	for _, entry := range Schema {
		if _, ok := overridden[Namespace+"."+entry.Key]; ok {
			locked = append(locked, entry.Key)
		}
	}
	return locked, nil
}

func validate(entry manifest.ConfigEntry, value any) error {
	invalid := func(want string) error {
		return fmt.Errorf("%w: %s must be %s, got %T", ErrInvalidValue, entry.Key, want, value)
	}

	switch entry.Type {
	case "boolean":
		if _, ok := value.(bool); !ok {
			return invalid("a boolean")
		}
	case "integer":
		if _, ok := asInt(value); !ok {
			return invalid("an integer")
		}
	case "string":
		s, ok := value.(string)
		if !ok {
			return invalid("a string")
		}
		if len(entry.Options) > 0 && !slices.ContainsFunc(entry.Options, func(o manifest.FieldOption) bool { return o.Value == s }) {
			return fmt.Errorf("%w: %s must be one of its declared options, got %q", ErrInvalidValue, entry.Key, s)
		}
	case "json":
		defaults, ok := value.(map[string][]string)
		if !ok {
			return invalid("a map[string][]string")
		}
		return validateDefaults(defaults)
	}
	return nil
}

// asInt also accepts a whole-number float64, the form a decoded JSON
// request body carries.
func asInt(value any) (int, bool) {
	switch n := value.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n == math.Trunc(n) {
			return int(n), true
		}
	}
	return 0, false
}

var knownChannels = []string{notifications.ChannelInApp, notifications.ChannelEmail, notifications.ChannelSMS, notifications.ChannelPush}

func validateDefaults(defaults map[string][]string) error {
	for notificationType, channels := range defaults {
		for _, ch := range channels {
			if !slices.Contains(knownChannels, ch) {
				return fmt.Errorf("%w: %s[%q] names unknown channel %q", ErrInvalidValue, KeyDefaults, notificationType, ch)
			}
		}
	}
	return nil
}
