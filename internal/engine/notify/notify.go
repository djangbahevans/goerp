// Package notify is the core of notify.Send (notification-system.md §4):
// validate a notification's type, route it to channels (§8), and in one
// transaction insert its feed row, one notification_deliveries row per
// channel recipient, and each non-in_app channel's delivery job — then
// push the new feed entry to the recipient's open sessions (§9).
//
// Module code reaches it through host.notify.send (goerp#1288); the engine
// calls it directly for its own engine.* types. The two differ only in
// whose declaration the type is checked against.
package notify

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/ws"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
)

// Priorities a notification type or a send can ask for.
const (
	PriorityNormal = "normal"
	PriorityHigh   = "high"
)

// Delivery job types a provider connector handles (connector-guide.md §8,
// §9).
const (
	JobTypeSMSSend  = "sms_send"
	JobTypePushSend = "push_send"
)

// providerPayloadSchemaVersion is the schema_version every provider
// category payload carries (host-abi-reference.md §10).
const providerPayloadSchemaVersion = 1

var (
	// ErrUnknownUser: the recipient is not a live user who belongs to the
	// tenant.
	ErrUnknownUser = errors.New("notification recipient is not a user of this tenant")
	// ErrInvalidPriority: Options.Priority is neither normal nor high.
	ErrInvalidPriority = errors.New("notification priority must be normal or high")
)

// Options are a send's call-site options (notification-system.md §7
// "Notify options").
type Options struct {
	// ForceChannels are delivered whatever the user's preferences.
	ForceChannels []string
	// AdditionalChannels are delivered unless the user turned them off.
	AdditionalChannels []string
	// ActionURL replaces the in_app template's action_url.
	ActionURL string
	// Priority overrides the type's default_priority.
	Priority string
	// TraceID is stamped onto the delivery jobs.
	TraceID string
}

// Result is what a send reports back: the feed row's ID and every channel
// a delivery was created for, in_app first.
type Result struct {
	NotificationID string
	ChannelsUsed   []string

	tenantID string
	userID   string
	event    feedEvent
}

// feedEvent is the notification.new payload (notification-system.md §9).
type feedEvent struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Module    string    `json:"module"`
	Title     string    `json:"title"`
	Body      *string   `json:"body"`
	ActionURL *string   `json:"action_url"`
	Icon      *string   `json:"icon"`
	CreatedAt time.Time `json:"created_at"`
}

// Registry supplies the current module registry snapshot — satisfied by
// *registry.ModuleRegistry.
type Registry interface {
	Snapshot() *registry.RegistrySnapshot
}

// ConfigLoader resolves a tenant's notification configuration — satisfied
// by *notifconfig.Service.
type ConfigLoader interface {
	Load(ctx context.Context, tenantID string) (*notifconfig.Config, error)
}

// ProviderResolver answers which module is a tenant's active provider for
// a single-active category — satisfied by *providerselect.Store.
type ProviderResolver interface {
	Resolve(ctx context.Context, tenantID, category string) (string, error)
}

// TenantLookup finds a tenant by ID — satisfied by *tenant.Store.
type TenantLookup interface {
	GetByID(ctx context.Context, id string) (*tenant.Tenant, error)
}

// MembershipChecker reports whether a user belongs to a tenant — satisfied
// by *role.Store.
type MembershipChecker interface {
	IsMember(ctx context.Context, tenantSlug, userID string) (bool, error)
}

// Broadcaster pushes a message to one user's subscribers of a channel in
// one tenant — satisfied by *ws.Hub.
type Broadcaster interface {
	BroadcastUser(ctx context.Context, channel, tenantID, userID, msgType string, payload any) (int, error)
}

// Deps are a Sender's collaborators. Jobs is the never-started,
// database/sql River client the engine inserts transactional jobs with
// (wasm.Runtime.EventInsertClient).
type Deps struct {
	DB        *sql.DB
	Store     *notifications.Store
	Registry  Registry
	Config    ConfigLoader
	Providers ProviderResolver
	Tenants   TenantLookup
	Members   MembershipChecker
	Jobs      *river.Client[*sql.Tx]
	Hub       Broadcaster
}

type Sender struct {
	Deps
}

func NewSender(d Deps) *Sender {
	return &Sender{Deps: d}
}

// Send sends notificationType, as moduleName emits it, to userID in
// tenantID, rendering its templates against data. Everything it writes
// commits together or not at all; once committed, the new feed entry is
// pushed to the user's open sessions.
func (s *Sender) Send(ctx context.Context, tenantID, moduleName, notificationType, userID string, data map[string]any, opts Options) (*Result, error) {
	p, err := s.prepare(ctx, tenantID, moduleName, notificationType, userID, data, opts)
	if err != nil {
		return nil, err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("send notification: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := s.write(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("send notification: %w", err)
	}
	s.Announce(ctx, res)
	return res, nil
}

// SendTx is Send on the caller's transaction: nothing it writes, jobs
// included, is visible unless tx commits. It does not push the new feed
// entry; call Announce once tx has committed.
func (s *Sender) SendTx(ctx context.Context, tx *sql.Tx, tenantID, moduleName, notificationType, userID string, data map[string]any, opts Options) (*Result, error) {
	p, err := s.prepare(ctx, tenantID, moduleName, notificationType, userID, data, opts)
	if err != nil {
		return nil, err
	}
	return s.write(ctx, tx, p)
}

// preparedSend is everything a send writes, resolved before its
// transaction opens.
type preparedSend struct {
	tenant           *tenant.Tenant
	userID           string
	moduleName       string
	notificationType string
	data             map[string]any
	cfg              *notifconfig.Config
	plan             []channelPlan
	content          inAppContent
	priority         string
	traceID          string
}

// prepare validates a send, routes it and renders its in_app content. It
// only reads, so Send runs it outside the transaction.
func (s *Sender) prepare(ctx context.Context, tenantID, moduleName, notificationType, userID string, data map[string]any, opts Options) (*preparedSend, error) {
	if err := validateChannels(opts.ForceChannels); err != nil {
		return nil, err
	}
	if err := validateChannels(opts.AdditionalChannels); err != nil {
		return nil, err
	}
	snapshot := s.Registry.Snapshot()
	nt, err := lookupType(snapshot, moduleName, notificationType)
	if err != nil {
		return nil, err
	}
	priority := cmp.Or(opts.Priority, nt.DefaultPriority, PriorityNormal)
	if priority != PriorityNormal && priority != PriorityHigh {
		return nil, fmt.Errorf("%w: %q", ErrInvalidPriority, priority)
	}

	t, err := s.Tenants.GetByID(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load tenant: %w", err)
	}
	user, err := s.loadRecipient(ctx, t.Slug, userID)
	if err != nil {
		return nil, err
	}
	cfg, err := s.Config.Load(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load notification config: %w", err)
	}
	prefs, err := s.Store.Preferences(ctx, t.Slug, tenantID, userID)
	if err != nil {
		return nil, err
	}

	routed := route(routeInput{
		notificationType: notificationType,
		manifestDefaults: nt.DefaultChannels,
		available:        nt.AvailableChannels,
		prefs:            prefs,
		config:           cfg,
		force:            opts.ForceChannels,
		additional:       opts.AdditionalChannels,
	})
	plan, err := s.planDeliveries(ctx, t, user, cfg, routed)
	if err != nil {
		return nil, err
	}

	content, err := renderInApp(snapshot, moduleName, nt.Name, nt.Label, user.locale, templateVars(data, t, user, opts))
	if err != nil {
		return nil, err
	}
	if opts.ActionURL != "" {
		content.ActionURL = opts.ActionURL
	}

	return &preparedSend{
		tenant: t, userID: userID, moduleName: moduleName, notificationType: notificationType, data: data,
		cfg: cfg, plan: plan, content: content, priority: priority, traceID: opts.TraceID,
	}, nil
}

// write inserts p's feed row, its delivery rows and its delivery jobs on
// tx.
func (s *Sender) write(ctx context.Context, tx *sql.Tx, p *preparedSend) (*Result, error) {
	t := p.tenant
	n, err := notifications.CreateTx(ctx, tx, t.Slug, notifications.NewNotification{
		TenantID:  t.ID,
		UserID:    p.userID,
		Type:      p.notificationType,
		Module:    p.moduleName,
		Title:     p.content.Title,
		Body:      nonEmpty(p.content.Body),
		ActionURL: nonEmpty(p.content.ActionURL),
		Icon:      nonEmpty(p.content.Icon),
		Data:      p.data,
	})
	if err != nil {
		return nil, err
	}

	var rows []notifications.NewDelivery
	for _, cp := range p.plan {
		for _, r := range cp.recipients {
			rows = append(rows, notifications.NewDelivery{Channel: cp.channel, Recipient: r.address, Provider: cp.provider})
		}
	}
	deliveries, err := notifications.CreateDeliveriesTx(ctx, tx, t.Slug, t.ID, n.ID, rows)
	if err != nil {
		return nil, err
	}
	if err := s.enqueueTx(ctx, tx, t, p.moduleName, n.ID, p.cfg, p.plan, deliveries, p.priority, p.traceID); err != nil {
		return nil, err
	}

	used := make([]string, len(p.plan))
	for i, cp := range p.plan {
		used[i] = cp.channel
	}
	return &Result{
		NotificationID: n.ID,
		ChannelsUsed:   used,
		tenantID:       t.ID,
		userID:         p.userID,
		event: feedEvent{
			ID: n.ID, Type: n.Type, Module: n.Module, Title: n.Title,
			Body: n.Body, ActionURL: n.ActionURL, Icon: n.Icon, CreatedAt: n.CreatedAt,
		},
	}, nil
}

// Announce pushes res's new feed entry to its recipient's open sessions in
// its tenant as notification.new. A session that misses it still finds
// the notification in its feed, so a failed push is only logged.
func (s *Sender) Announce(ctx context.Context, res *Result) {
	if s.Hub == nil || res == nil {
		return
	}
	if _, err := s.Hub.BroadcastUser(ctx, ws.NotificationsChannel, res.tenantID, res.userID, "notification.new", res.event); err != nil {
		log.Debug().Err(err).Str("notification_id", res.NotificationID).Msg("notify: notification.new push reached no session")
	}
}

// recipient is the user a notification goes to, as delivery needs them.
type recipient struct {
	id     string
	email  string
	name   string
	locale string
	phone  string
}

// loadRecipient loads userID, who must be a member of the tenant: a
// notification carries the tenant's data to wherever it is delivered.
func (s *Sender) loadRecipient(ctx context.Context, tenantSlug, userID string) (*recipient, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownUser, userID)
	}
	member, err := s.Members.IsMember(ctx, tenantSlug, userID)
	if err != nil {
		return nil, fmt.Errorf("check notification recipient membership: %w", err)
	}
	if !member {
		return nil, fmt.Errorf("%w: %q", ErrUnknownUser, userID)
	}
	u := &recipient{id: userID}
	err = s.DB.QueryRowContext(ctx, `
		SELECT u.email, COALESCE(p.name, ''), COALESCE(p.locale, ''), COALESCE(p.phone, '')
		FROM system.users u
		LEFT JOIN system.user_profiles p ON p.user_id = u.id
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`, userID).Scan(&u.email, &u.name, &u.locale, &u.phone)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownUser, userID)
	}
	if err != nil {
		return nil, fmt.Errorf("load notification recipient: %w", err)
	}
	return u, nil
}

// channelPlan is one routed channel's deliveries: every address it goes
// to, and the provider that sends it (the email adapter's name, or the
// connector module a provider category resolved to).
type channelPlan struct {
	channel    string
	provider   string
	recipients []planRecipient
}

type planRecipient struct {
	address  string
	platform string // push only
}

// planDeliveries is routing step 5: it keeps each routed channel that can
// actually reach user, with its recipients, and drops the rest.
func (s *Sender) planDeliveries(ctx context.Context, t *tenant.Tenant, user *recipient, cfg *notifconfig.Config, channels []string) ([]channelPlan, error) {
	var plan []channelPlan
	for _, ch := range channels {
		switch ch {
		case notifications.ChannelInApp:
			plan = append(plan, channelPlan{channel: ch, recipients: []planRecipient{{address: user.id}}})

		case notifications.ChannelEmail:
			if user.email == "" || !emailConfigured(cfg.Email) {
				continue
			}
			plan = append(plan, channelPlan{channel: ch, provider: cfg.Email.Provider, recipients: []planRecipient{{address: user.email}}})

		case notifications.ChannelSMS:
			if user.phone == "" {
				continue
			}
			provider, err := s.resolveProvider(ctx, t.ID, providerselect.CategorySMS)
			if err != nil {
				return nil, err
			}
			if provider == "" {
				continue
			}
			plan = append(plan, channelPlan{channel: ch, provider: provider, recipients: []planRecipient{{address: user.phone}}})

		case notifications.ChannelPush:
			provider, err := s.resolveProvider(ctx, t.ID, providerselect.CategoryPush)
			if err != nil {
				return nil, err
			}
			if provider == "" {
				continue
			}
			tokens, err := s.Store.DeviceTokens(ctx, t.Slug, t.ID, user.id)
			if err != nil {
				return nil, err
			}
			if len(tokens) == 0 {
				continue
			}
			p := channelPlan{channel: ch, provider: provider}
			for _, tok := range tokens {
				p.recipients = append(p.recipients, planRecipient{address: tok.Token, platform: tok.Platform})
			}
			plan = append(plan, p)
		}
	}
	return plan, nil
}

// resolveProvider returns the tenant's active provider for category, or ""
// when there is none to deliver through.
func (s *Sender) resolveProvider(ctx context.Context, tenantID, category string) (string, error) {
	module, err := s.Providers.Resolve(ctx, tenantID, category)
	switch {
	case err == nil:
		return module, nil
	case errors.Is(err, providerselect.ErrNoProviderInstalled), errors.Is(err, providerselect.ErrNoProviderSelected):
		return "", nil
	default:
		return "", fmt.Errorf("resolve %s: %w", category, err)
	}
}

// emailConfigured reports whether the tenant's email adapter has what it
// needs to send at all.
func emailConfigured(c notifconfig.EmailConfig) bool {
	switch c.Provider {
	case notifconfig.ProviderResend:
		return c.APIKey != ""
	case notifconfig.ProviderSMTP:
		return c.SMTP.Host != ""
	default:
		return false
	}
}

// smsSendPayload is the sms_send job's payload. The connector-facing
// contract, rendered body included, is goerp#1287's.
type smsSendPayload struct {
	SchemaVersion  int    `msgpack:"schema_version"`
	TenantID       string `msgpack:"tenant_id"`
	NotificationID string `msgpack:"notification_id"`
	To             string `msgpack:"to"`
	From           string `msgpack:"from"`
	IdempotencyKey string `msgpack:"idempotency_key"`
}

// pushSendPayload is the push_send job's payload: every device token the
// user has, each its own delivery. The connector-facing contract, rendered
// title and body included, is goerp#1287's.
type pushSendPayload struct {
	SchemaVersion  int         `msgpack:"schema_version"`
	TenantID       string      `msgpack:"tenant_id"`
	NotificationID string      `msgpack:"notification_id"`
	Tokens         []pushToken `msgpack:"tokens"`
}

type pushToken struct {
	Platform       string `msgpack:"platform"`
	Token          string `msgpack:"token"`
	IdempotencyKey string `msgpack:"idempotency_key"`
}

// enqueueTx inserts each non-in_app channel's delivery job on tx: an
// email_send per email delivery, and one provider-category job per sms or
// push channel, dispatched to the provider planDeliveries resolved.
func (s *Sender) enqueueTx(ctx context.Context, tx *sql.Tx, t *tenant.Tenant, moduleName, notificationID string, cfg *notifconfig.Config, plan []channelPlan, deliveries []notifications.Delivery, priority, traceID string) error {
	riverPriority := 3
	if priority == PriorityHigh {
		riverPriority = 1
	}

	byChannel := map[string][]notifications.Delivery{}
	for _, d := range deliveries {
		byChannel[d.Channel] = append(byChannel[d.Channel], d)
	}

	for _, p := range plan {
		ds := byChannel[p.channel]
		switch p.channel {
		case notifications.ChannelEmail:
			for _, d := range ds {
				args := jobqueue.EmailSendArgs{
					TenantID: t.ID, TenantSlug: t.Slug, NotificationID: notificationID,
					DeliveryID: d.ID, Recipient: d.Recipient, IdempotencyKey: d.IdempotencyKey,
				}
				if _, err := s.Jobs.InsertTx(ctx, tx, args, &river.InsertOpts{Priority: riverPriority}); err != nil {
					return fmt.Errorf("enqueue email_send: %w", err)
				}
			}

		case notifications.ChannelSMS:
			d := ds[0]
			payload := smsSendPayload{
				SchemaVersion: providerPayloadSchemaVersion, TenantID: t.ID, NotificationID: notificationID,
				To: d.Recipient, From: cfg.SMS.SenderID, IdempotencyKey: d.IdempotencyKey,
			}
			if err := s.insertProviderJob(ctx, tx, t.ID, moduleName, providerselect.CategorySMS, JobTypeSMSSend, p.provider, payload, riverPriority, traceID); err != nil {
				return err
			}

		case notifications.ChannelPush:
			payload := pushSendPayload{SchemaVersion: providerPayloadSchemaVersion, TenantID: t.ID, NotificationID: notificationID}
			for i, d := range ds {
				payload.Tokens = append(payload.Tokens, pushToken{Platform: p.recipients[i].platform, Token: d.Recipient, IdempotencyKey: d.IdempotencyKey})
			}
			if err := s.insertProviderJob(ctx, tx, t.ID, moduleName, providerselect.CategoryPush, JobTypePushSend, p.provider, payload, riverPriority, traceID); err != nil {
				return err
			}
		}
	}
	return nil
}

// insertProviderJob inserts a provider-category job for providerModule the
// way host.jobs.enqueue_provider_tx does, on behalf of the emitting module.
func (s *Sender) insertProviderJob(ctx context.Context, tx *sql.Tx, tenantID, moduleName, category, jobType, providerModule string, payload any, riverPriority int, traceID string) error {
	encoded, err := msgpack.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s payload: %w", jobType, err)
	}
	metadata, err := json.Marshal(jobqueue.JobMetadata{TenantID: tenantID, ModuleName: providerModule, TraceID: traceID, EnqueuedBy: moduleName})
	if err != nil {
		return fmt.Errorf("encode %s metadata: %w", jobType, err)
	}
	args := jobqueue.WASMJobArgs{
		ModuleName:       providerModule,
		JobType:          jobType,
		Payload:          encoded,
		TenantID:         tenantID,
		Queue:            jobqueue.QueueDefault,
		MaxAttempts:      jobqueue.NotificationDeliveryMaxAttempts,
		TraceID:          traceID,
		ProviderCategory: category,
		EnqueuedBy:       moduleName,
	}
	opts := &river.InsertOpts{
		Queue:       jobqueue.QueueDefault,
		Priority:    riverPriority,
		MaxAttempts: jobqueue.NotificationDeliveryMaxAttempts,
		Metadata:    metadata,
	}
	if _, err := s.Jobs.InsertTx(ctx, tx, args, opts); err != nil {
		return fmt.Errorf("enqueue %s: %w", jobType, err)
	}
	return nil
}

// templateVars is data plus the engine-injected variables
// (notification-system.md §5 "Standard template variables") the in_app
// template can use; an injected variable wins over a data field of the
// same name.
func templateVars(data map[string]any, t *tenant.Tenant, user *recipient, opts Options) map[string]any {
	vars := make(map[string]any, len(data)+5)
	maps.Copy(vars, data)
	firstName, _, _ := strings.Cut(user.name, " ")
	vars["TenantName"] = t.Name
	vars["UserName"] = user.name
	vars["UserFirstName"] = firstName
	vars["Year"] = time.Now().Year()
	if opts.ActionURL != "" {
		vars["ActionURL"] = opts.ActionURL
	}
	return vars
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
