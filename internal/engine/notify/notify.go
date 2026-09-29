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

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
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
	JobTypeSMSSend  = abiv1.JobTypeSMSSend
	JobTypePushSend = abiv1.JobTypePushSend
)

var (
	// ErrUnknownUser: the recipient is not a live user who belongs to the
	// tenant.
	ErrUnknownUser = errors.New("notification recipient is not a user of this tenant")
	// ErrInvalidPriority: Options.Priority is neither normal nor high.
	ErrInvalidPriority = errors.New("notification priority must be normal or high")
	// ErrTooManyRecipients: a SendBulk names more than MaxBulkRecipients
	// users.
	ErrTooManyRecipients = fmt.Errorf("a bulk notification goes to at most %d users", MaxBulkRecipients)
	// ErrRenderFailed: a template failed to render against the send's
	// data, so resending the same data fails the same way.
	ErrRenderFailed = errors.New("notification template could not be rendered")
	// ErrTxAborted: SendTx could not restore the caller's transaction, which
	// can no longer be used.
	ErrTxAborted = errors.New("notification transaction is aborted")
)

// MaxBulkRecipients caps one SendBulk's recipients (host-abi-reference.md
// §11 "host.notify.send_bulk"); a caller with more batches them through
// background jobs.
const MaxBulkRecipients = abiv1.NotifyMaxBulkRecipients

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
	// IdempotencyKey, when set, makes the send happen at most once per
	// recipient: a later send from the same module to the same user with
	// the same key creates nothing and reports the first one's
	// notification.
	IdempotencyKey string
}

// Result is what a send reports back: the feed row's ID and every channel
// a delivery was created for, in_app first. Deduplicated is set when the
// send's IdempotencyKey matched an earlier notification, which is the one
// reported.
type Result struct {
	UserID         string
	NotificationID string
	ChannelsUsed   []string
	Deduplicated   bool

	tenantID string
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
	results, err := s.SendBulk(ctx, tenantID, moduleName, notificationType, []string{userID}, data, opts)
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

// SendTx is Send on the caller's transaction: nothing it writes, jobs
// included, is visible unless tx commits. A failed SendTx rolls back to a
// savepoint, leaving tx as it found it; only an error wrapping
// ErrTxAborted means tx can no longer be used. It does not push the new
// feed entry; call Announce once tx has committed.
func (s *Sender) SendTx(ctx context.Context, tx *sql.Tx, tenantID, moduleName, notificationType, userID string, data map[string]any, opts Options) (*Result, error) {
	spec, err := s.prepareSend(ctx, tenantID, moduleName, notificationType, opts)
	if err != nil {
		return nil, err
	}
	p, err := s.prepareRecipient(ctx, spec, userID, data, opts)
	if err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, "SAVEPOINT notify_send"); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTxAborted, err)
	}
	res, err := s.write(ctx, tx, p)
	if err != nil {
		if _, rbErr := tx.ExecContext(context.WithoutCancel(ctx), "ROLLBACK TO SAVEPOINT notify_send"); rbErr != nil {
			return nil, errors.Join(err, fmt.Errorf("%w: %w", ErrTxAborted, rbErr))
		}
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT notify_send"); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTxAborted, err)
	}
	return res, nil
}

// SendBulk is Send to each of userIDs, at most MaxBulkRecipients, with the
// same data for every one. A user listed twice is sent to once. Every
// recipient is validated and routed before anything is written, and every
// notification commits together or not at all; the results are in
// userIDs' order.
func (s *Sender) SendBulk(ctx context.Context, tenantID, moduleName, notificationType string, userIDs []string, data map[string]any, opts Options) ([]*Result, error) {
	userIDs = uniqueUsers(userIDs)
	if len(userIDs) > MaxBulkRecipients {
		return nil, fmt.Errorf("%w: got %d", ErrTooManyRecipients, len(userIDs))
	}
	spec, err := s.prepareSend(ctx, tenantID, moduleName, notificationType, opts)
	if err != nil {
		return nil, err
	}
	if len(userIDs) == 0 {
		return nil, nil
	}
	prepared := make([]*preparedSend, len(userIDs))
	for i, userID := range userIDs {
		if prepared[i], err = s.prepareRecipient(ctx, spec, userID, data, opts); err != nil {
			return nil, err
		}
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("send notification: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	results := make([]*Result, len(prepared))
	for i, p := range prepared {
		if results[i], err = s.write(ctx, tx, p); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("send notification: %w", err)
	}
	for _, res := range results {
		s.Announce(ctx, res)
	}
	return results, nil
}

// sendSpec is what a send resolves once, whoever it goes to.
type sendSpec struct {
	snapshot         *registry.RegistrySnapshot
	tenant           *tenant.Tenant
	cfg              *notifconfig.Config
	moduleName       string
	notificationType string
	declared         manifest.NotificationType
	priority         string

	// providers memoizes resolveProvider by category: every recipient of a
	// send shares its tenant's provider.
	providers map[string]string
}

// preparedSend is everything one recipient's send writes, resolved before
// its transaction opens.
type preparedSend struct {
	*sendSpec
	userID         string
	data           map[string]any
	plan           []channelPlan
	content        inAppContent
	provider       providerContent
	traceID        string
	idempotencyKey string
}

// prepareSend validates a send's type and options and loads its tenant
// and the tenant's notification configuration.
func (s *Sender) prepareSend(ctx context.Context, tenantID, moduleName, notificationType string, opts Options) (*sendSpec, error) {
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
	cfg, err := s.Config.Load(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load notification config: %w", err)
	}
	return &sendSpec{
		snapshot: snapshot, tenant: t, cfg: cfg,
		moduleName: moduleName, notificationType: notificationType, declared: nt, priority: priority,
		providers: make(map[string]string),
	}, nil
}

// prepareRecipient routes spec's send to userID and renders its in_app
// content. It only reads, so a send runs it outside the transaction.
func (s *Sender) prepareRecipient(ctx context.Context, spec *sendSpec, userID string, data map[string]any, opts Options) (*preparedSend, error) {
	t := spec.tenant
	user, err := s.loadRecipient(ctx, t.Slug, userID)
	if err != nil {
		return nil, err
	}
	prefs, err := s.Store.Preferences(ctx, t.Slug, t.ID, userID)
	if err != nil {
		return nil, err
	}

	routed := route(routeInput{
		notificationType: spec.notificationType,
		manifestDefaults: spec.declared.DefaultChannels,
		available:        spec.declared.AvailableChannels,
		prefs:            prefs,
		config:           spec.cfg,
		force:            opts.ForceChannels,
		additional:       opts.AdditionalChannels,
	})
	plan, err := s.planDeliveries(ctx, spec, user, routed)
	if err != nil {
		return nil, err
	}

	vars := templateVars(data, t, user, opts)
	content, err := renderInApp(spec.snapshot, spec.moduleName, spec.declared.Name, spec.declared.Label, user.locale, vars)
	if err != nil {
		return nil, err
	}
	if opts.ActionURL != "" {
		content.ActionURL = opts.ActionURL
	}
	provider := renderProviderChannels(spec.snapshot, spec.moduleName, spec.declared.Name, user.locale, plan, content, vars)

	return &preparedSend{
		sendSpec: spec, userID: userID, data: data, plan: plan, content: content, provider: provider,
		traceID: opts.TraceID, idempotencyKey: opts.IdempotencyKey,
	}, nil
}

// write inserts p's feed row, its delivery rows and its delivery jobs on
// tx.
func (s *Sender) write(ctx context.Context, tx *sql.Tx, p *preparedSend) (*Result, error) {
	t := p.tenant
	n, created, err := notifications.CreateTx(ctx, tx, t.Slug, notifications.NewNotification{
		TenantID:  t.ID,
		UserID:    p.userID,
		Type:      p.notificationType,
		Module:    p.moduleName,
		Title:     p.content.Title,
		Body:      nonEmpty(p.content.Body),
		ActionURL: nonEmpty(p.content.ActionURL),
		Icon:      nonEmpty(p.content.Icon),
		Data:      p.data,

		IdempotencyKey: p.idempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	if !created {
		used, err := notifications.DeliveryChannelsTx(ctx, tx, t.Slug, n.ID)
		if err != nil {
			return nil, err
		}
		return &Result{UserID: p.userID, NotificationID: n.ID, ChannelsUsed: used, Deduplicated: true}, nil
	}

	var rows []notifications.NewDelivery
	for _, cp := range p.plan {
		var reason string
		if err := p.provider.failed[cp.channel]; err != nil {
			reason = err.Error()
		}
		for _, r := range cp.recipients {
			rows = append(rows, notifications.NewDelivery{Channel: cp.channel, Recipient: r.address, Provider: cp.provider, FailureReason: reason})
		}
	}
	deliveries, err := notifications.CreateDeliveriesTx(ctx, tx, t.Slug, t.ID, n.ID, rows)
	if err != nil {
		return nil, err
	}
	if err := s.enqueueTx(ctx, tx, p, n.ID, deliveries); err != nil {
		return nil, err
	}

	used := make([]string, len(p.plan))
	for i, cp := range p.plan {
		used[i] = cp.channel
	}
	return &Result{
		UserID:         p.userID,
		NotificationID: n.ID,
		ChannelsUsed:   used,
		tenantID:       t.ID,
		event: feedEvent{
			ID: n.ID, Type: n.Type, Module: n.Module, Title: n.Title,
			Body: n.Body, ActionURL: n.ActionURL, Icon: n.Icon, CreatedAt: n.CreatedAt,
		},
	}, nil
}

// Announce pushes res's new feed entry to its recipient's open sessions in
// its tenant as notification.new. A session that misses it still finds
// the notification in its feed, so a failed push is only logged. A
// deduplicated send created nothing new and pushes nothing.
func (s *Sender) Announce(ctx context.Context, res *Result) {
	if s.Hub == nil || res == nil || res.Deduplicated {
		return
	}
	if _, err := s.Hub.BroadcastUser(ctx, ws.NotificationsChannel, res.tenantID, res.UserID, "notification.new", res.event); err != nil {
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
func (s *Sender) planDeliveries(ctx context.Context, spec *sendSpec, user *recipient, channels []string) ([]channelPlan, error) {
	t, cfg := spec.tenant, spec.cfg
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
			provider, err := s.resolveProvider(ctx, spec, providerselect.CategorySMS)
			if err != nil {
				return nil, err
			}
			if provider == "" {
				continue
			}
			plan = append(plan, channelPlan{channel: ch, provider: provider, recipients: []planRecipient{{address: user.phone}}})

		case notifications.ChannelPush:
			provider, err := s.resolveProvider(ctx, spec, providerselect.CategoryPush)
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

// resolveProvider returns spec's tenant's active provider for category, or
// "" when there is none to deliver through.
func (s *Sender) resolveProvider(ctx context.Context, spec *sendSpec, category string) (string, error) {
	if module, ok := spec.providers[category]; ok {
		return module, nil
	}
	module, err := s.Providers.Resolve(ctx, spec.tenant.ID, category)
	switch {
	case err == nil:
	case errors.Is(err, providerselect.ErrNoProviderInstalled), errors.Is(err, providerselect.ErrNoProviderSelected):
		module = ""
	default:
		return "", fmt.Errorf("resolve %s: %w", category, err)
	}
	spec.providers[category] = module
	return module, nil
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

// enqueueTx inserts each non-in_app channel's delivery job for p's
// notification on tx: an email_send per email delivery, and one
// provider-category job per sms or push channel, dispatched to the
// provider planDeliveries resolved.
func (s *Sender) enqueueTx(ctx context.Context, tx *sql.Tx, p *preparedSend, notificationID string, deliveries []notifications.Delivery) error {
	t := p.tenant
	riverPriority := 3
	if p.priority == PriorityHigh {
		riverPriority = 1
	}

	byChannel := map[string][]notifications.Delivery{}
	for _, d := range deliveries {
		byChannel[d.Channel] = append(byChannel[d.Channel], d)
	}

	for _, cp := range p.plan {
		if err := p.provider.failed[cp.channel]; err != nil {
			log.Warn().Err(err).Str("tenant_id", t.ID).Str("notification_id", notificationID).Str("channel", cp.channel).
				Msg("notify: template did not render, channel's deliveries failed")
			continue
		}
		ds := byChannel[cp.channel]
		switch cp.channel {
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
			payload := abiv1.SMSSendPayload{
				SchemaVersion: abiv1.ProviderPayloadSchemaVersion, TenantID: t.ID, NotificationID: notificationID,
				To: d.Recipient, From: p.cfg.SMS.SenderID, Body: p.provider.smsBody, IdempotencyKey: d.IdempotencyKey,
			}
			if err := s.insertProviderJob(ctx, tx, p, notificationID, providerselect.CategorySMS, JobTypeSMSSend, cp.provider, payload, riverPriority); err != nil {
				return err
			}

		case notifications.ChannelPush:
			payload := abiv1.PushSendPayload{
				SchemaVersion: abiv1.ProviderPayloadSchemaVersion, TenantID: t.ID, NotificationID: notificationID,
				Title: p.provider.pushTitle, Body: p.provider.pushBody, ActionURL: p.content.ActionURL,
				Data: map[string]string{"notification_id": notificationID, "type": p.notificationType},
			}
			for i, d := range ds {
				payload.Tokens = append(payload.Tokens, abiv1.PushDeviceToken{Platform: cp.recipients[i].platform, Token: d.Recipient, IdempotencyKey: d.IdempotencyKey})
			}
			if err := s.insertProviderJob(ctx, tx, p, notificationID, providerselect.CategoryPush, JobTypePushSend, cp.provider, payload, riverPriority); err != nil {
				return err
			}
		}
	}
	return nil
}

// insertProviderJob inserts a provider-category job for providerModule the
// way host.jobs.enqueue_provider_tx does, on behalf of p's emitting module.
// Its NotificationID is what has jobdispatch.Worker record the job's
// outcome on the notification's deliveries.
func (s *Sender) insertProviderJob(ctx context.Context, tx *sql.Tx, p *preparedSend, notificationID, category, jobType, providerModule string, payload any, riverPriority int) error {
	tenantID, moduleName, traceID := p.tenant.ID, p.moduleName, p.traceID
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
		NotificationID:   notificationID,
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

// uniqueUsers returns userIDs without its repeats, in first-seen order,
// each valid UUID in its canonical form so that two spellings of one user
// are one recipient.
func uniqueUsers(userIDs []string) []string {
	seen := make(map[string]bool, len(userIDs))
	out := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		if u, err := uuid.Parse(id); err == nil {
			id = u.String()
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
