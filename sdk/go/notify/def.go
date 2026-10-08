package notify

import (
	"errors"
	"fmt"
	"regexp"
	"slices"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// Priority is a notification type's default priority.
type Priority string

// The priorities a notification type can default to.
const (
	Normal Priority = "normal"
	High   Priority = "high"
)

var (
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	channels    = []string{ChannelInApp, ChannelEmail, ChannelSMS, ChannelPush}
	defined     = map[string]bool{}
)

type definition struct {
	label             string
	description       string
	defaultChannels   []string
	availableChannels []string
	priority          Priority
	templates         map[string]string
}

// DefineOption configures Define.
type DefineOption func(*definition)

// Label is the type's human-readable name in preferences and the admin UI.
// It is required.
func Label(text string) DefineOption { return func(d *definition) { d.label = text } }

// Description says when the notification is sent.
func Description(text string) DefineOption { return func(d *definition) { d.description = text } }

// DefaultChannels are the channels enabled for a user who has not chosen.
// They must include ChannelInApp and be among the AvailableChannels; unset,
// only ChannelInApp.
func DefaultChannels(channels ...string) DefineOption {
	return func(d *definition) { d.defaultChannels = channels }
}

// AvailableChannels are the channels a user can toggle in their preferences.
// They must include ChannelInApp and every default channel; unset, only
// ChannelInApp.
func AvailableChannels(channels ...string) DefineOption {
	return func(d *definition) { d.availableChannels = channels }
}

// DefaultPriority sets the priority a send uses unless it names one with
// HighPriority or NormalPriority. Unset, Normal.
func DefaultPriority(p Priority) DefineOption { return func(d *definition) { d.priority = p } }

// Template overrides the bundled template path of channel; "{locale}" in path
// is replaced with the locale at load time.
func Template(channel, path string) DefineOption {
	return func(d *definition) {
		if d.templates == nil {
			d.templates = map[string]string{}
		}
		d.templates[channel] = path
	}
}

// Def is a typed notification type definition binding the type's short name
// to D, the data its templates render against.
type Def[D any] struct {
	name string
}

// Define declares the notification type name, whose templates render against
// a D. name is the short name: the engine qualifies it with the calling
// module's name. It panics when name is not snake_case or is already
// defined in the module, no Label is given, a channel, priority or template
// is unknown, or the channel lists break the rules of manifest-spec.md §13a,
// so a bad definition fails when the module loads, not when it first sends.
func Define[D any](name string, opts ...DefineOption) Def[D] {
	d := definition{defaultChannels: []string{ChannelInApp}, availableChannels: []string{ChannelInApp}, priority: Normal}
	for _, opt := range opts {
		opt(&d)
	}
	if err := d.validate(name); err != nil {
		panic(fmt.Sprintf("notify.Define: %v", err))
	}
	defined[name] = true
	declareNotification[D](name, d)
	return Def[D]{name: name}
}

func (d definition) validate(name string) error {
	switch {
	case !namePattern.MatchString(name):
		return fmt.Errorf("name %q must be snake_case with no module prefix", name)
	case defined[name]:
		return fmt.Errorf("%q is already defined in this module", name)
	case d.label == "":
		return fmt.Errorf("%q needs notify.Label", name)
	case d.priority != Normal && d.priority != High:
		return fmt.Errorf("%q has unknown priority %q", name, d.priority)
	}
	for _, list := range [][]string{d.defaultChannels, d.availableChannels} {
		for _, ch := range list {
			if !slices.Contains(channels, ch) {
				return fmt.Errorf("%q names unknown channel %q", name, ch)
			}
		}
	}
	if !slices.Contains(d.availableChannels, ChannelInApp) || !slices.Contains(d.defaultChannels, ChannelInApp) {
		return fmt.Errorf("%q: default and available channels must both include %q", name, ChannelInApp)
	}
	for _, ch := range d.defaultChannels {
		if !slices.Contains(d.availableChannels, ch) {
			return fmt.Errorf("%q: default channel %q is not among its available channels", name, ch)
		}
	}
	for ch, path := range d.templates {
		if !slices.Contains(channels, ch) {
			return fmt.Errorf("%q has a template for unknown channel %q", name, ch)
		}
		if path == "" {
			return fmt.Errorf("%q has an empty %s template path", name, ch)
		}
	}
	return nil
}

// Name returns the type's short name.
func (d Def[D]) Name() string { return d.name }

// Send sends the notification to userID via host.notify.send, rendering its
// templates against data. Delivery is asynchronous: Send returns once the
// notification and its delivery jobs are written.
func (d Def[D]) Send(userID string, data D, opts ...NotifyOption) error {
	in, err := d.sendInput(userID, data, opts)
	if err != nil {
		return err
	}
	return hostcall.Do(hostNotifySend, in, &abi.NotifySendOutput{})
}

// SendTx is Send inside tx via host.notify.send_tx: the notification and
// its delivery jobs exist only if tx commits, and the recipient's open
// sessions hear of it only then.
func (d Def[D]) SendTx(tx *db.Tx, userID string, data D, opts ...NotifyOption) error {
	in, err := d.sendInput(userID, data, opts)
	if err != nil {
		return err
	}
	return hostcall.Do(hostNotifySendTx, abi.NotifySendTxInput{
		TxID: tx.TxID(), UserID: in.UserID, Type: in.Type, TemplateKey: in.TemplateKey, Data: in.Data, Opts: in.Opts,
	}, &abi.NotifySendOutput{})
}

// SendBulk is Send to each of userIDs, at most MaxBulkRecipients, with the
// same data for every one, via host.notify.send_bulk. Every notification is
// written together or none is: one user who isn't a member of the tenant
// fails the whole call. For per-recipient data, call Send for each user
// instead.
func (d Def[D]) SendBulk(userIDs []string, data D, opts ...NotifyOption) error {
	in, err := d.sendInput("", data, opts)
	if err != nil {
		return err
	}
	return hostcall.Do(hostNotifySendBulk, abi.NotifySendBulkInput{
		UserIDs: userIDs, Type: in.Type, TemplateKey: in.TemplateKey, Data: in.Data, Opts: in.Opts,
	}, &abi.NotifySendBulkOutput{})
}

// sendInput is the host.notify.send request for this type. Type carries the
// short name, which the host qualifies with the calling module's name; the
// template key is the same short name, since templates are resolved by type
// and channel.
func (d Def[D]) sendInput(userID string, data D, opts []NotifyOption) (abi.NotifySendInput, error) {
	if d.name == "" {
		return abi.NotifySendInput{}, errors.New("notify: send on a Def that notify.Define did not create")
	}
	encoded, err := encodeData(data)
	if err != nil {
		return abi.NotifySendInput{}, fmt.Errorf("notify %s: %w", d.name, err)
	}
	return abi.NotifySendInput{
		UserID: userID, Type: d.name, TemplateKey: d.name, Data: encoded, Opts: buildOptions(opts),
	}, nil
}
