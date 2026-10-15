// Package ui builds instructions for the shell to execute sequentially after an
// action. All instructions can be returned in an HTTP response; only Toast,
// Reload and SetBadge can be pushed to connected clients. Builders make no host
// calls and require no capability.
package ui

import "encoding/json/v2"

// Instruction is a shell command constructed by an instruction builder.
type Instruction struct {
	value any
}

func (i Instruction) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.value)
}

// Severity identifies a toast's notification level.
type Severity string

const (
	Success Severity = "success"
	Error   Severity = "error"
	Warning Severity = "warning"
	Info    Severity = "info"
)

type toastInstruction struct {
	Type       string   `json:"type"`
	Message    string   `json:"message"`
	Severity   Severity `json:"severity"`
	DurationMS *int     `json:"duration_ms,omitzero"`
}

// ToastOption configures a toast notification.
type ToastOption func(*toastInstruction)

// WithDuration overrides the shell's 4000 ms auto-dismiss duration. Zero keeps
// the toast visible until the user dismisses it.
func WithDuration(ms int) ToastOption {
	return func(i *toastInstruction) { i.DurationMS = new(ms) }
}

// Toast shows a notification banner with the given severity.
func Toast(message string, severity Severity, opts ...ToastOption) Instruction {
	i := toastInstruction{Type: "toast", Message: message, Severity: severity}
	for _, opt := range opts {
		opt(&i)
	}

	return Instruction{value: i}
}

type reloadInstruction struct {
	Type     string `json:"type"`
	Resource string `json:"resource,omitempty"`
	RecordID string `json:"record_id,omitempty"`
}

// ReloadOption restricts a reload to a view matching its target.
type ReloadOption func(*reloadInstruction)

// WithTarget reloads only when the current view is the form for this record.
func WithTarget(resource, recordID string) ReloadOption {
	return func(i *reloadInstruction) {
		i.Resource = resource
		i.RecordID = recordID
	}
}

// WithListTarget reloads only when the current view is a list of this resource.
func WithListTarget(resource string) ReloadOption {
	return func(i *reloadInstruction) {
		i.Resource = resource
		i.RecordID = ""
	}
}

// Reload invalidates and re-fetches the current view's data. A target that is
// not on screen is silently skipped.
func Reload(opts ...ReloadOption) Instruction {
	i := reloadInstruction{Type: "reload"}
	for _, opt := range opts {
		opt(&i)
	}

	return Instruction{value: i}
}

// SetBadge updates the cached count for a nav item or form tab. Target is its
// badge_count_route path with record placeholders resolved; count must be
// non-negative. A target with no rendered badge is ignored.
func SetBadge(target string, count int) Instruction {
	return Instruction{value: struct {
		Type   string `json:"type"`
		Target string `json:"target"`
		Count  int    `json:"count"`
	}{Type: "set_badge", Target: target, Count: count}}
}

// Navigate replaces the current view with an absolute path within the app.
// Path supports {record.field} interpolation from the current form's record.
func Navigate(path string) Instruction {
	return Instruction{value: struct {
		Type string `json:"type"`
		Path string `json:"path"`
	}{Type: "navigate", Path: path}}
}

type formInstruction struct {
	Type     string `json:"type"`
	Resource string `json:"resource"`
	RecordID string `json:"record_id,omitempty"`
}

// FormOption configures the record form opened in a modal.
type FormOption func(*formInstruction)

// WithRecordID opens an existing record instead of a new record form.
func WithRecordID(id string) FormOption {
	return func(i *formInstruction) { i.RecordID = id }
}

// OpenForm opens a resource's form in a modal, creating a new record unless
// WithRecordID selects an existing one. Resource is a module-qualified model ID.
func OpenForm(resource string, opts ...FormOption) Instruction {
	i := formInstruction{Type: "open_form", Resource: resource}
	for _, opt := range opts {
		opt(&i)
	}

	return Instruction{value: i}
}

// Dialog collects field values and submits them as the action's request body.
// The action response is executed as a new instruction sequence.
type Dialog struct {
	Title       string         `json:"title"`
	SubmitLabel string         `json:"submit_label,omitempty"`
	Action      Action         `json:"action"`
	Fields      []Field        `json:"fields"`
	Values      map[string]any `json:"values,omitempty"`
}

// Action identifies the HTTP route called on dialog submission. Path must be a
// concrete request path; the shell does not interpolate it.
type Action struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// Field defines a dialog input. Default supports {record.field} and {today}
// interpolation; Dialog.Values entries take precedence over defaults. Select
// fields require Options. Monetary values are submitted as minor units in the
// tenant's default currency.
type Field struct {
	Name        string         `json:"name"`
	Label       string         `json:"label"`
	Type        FieldType      `json:"type"`
	Required    bool           `json:"required,omitzero"`
	Placeholder string         `json:"placeholder,omitempty"`
	Default     string         `json:"default,omitempty"`
	Help        string         `json:"help,omitempty"`
	Options     []SelectOption `json:"options,omitempty"`
}

// SelectOption is a selectable value and its displayed label.
type SelectOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// FieldType identifies a dialog input, independently of model and view fields.
type FieldType string

const (
	FieldText     FieldType = "text"
	FieldTextarea FieldType = "textarea"
	FieldNumber   FieldType = "number"
	FieldMonetary FieldType = "monetary"
	FieldPhone    FieldType = "phone"
	FieldDate     FieldType = "date"
	FieldSelect   FieldType = "select"
	FieldBoolean  FieldType = "boolean"
)

// OpenDialog opens a data-collection dialog. An empty SubmitLabel leaves the
// shell's default label, "Submit".
func OpenDialog(dialog Dialog) Instruction {
	return Instruction{value: struct {
		Type string `json:"type"`
		Dialog
	}{Type: "open_dialog", Dialog: dialog}}
}

// CloseModal closes the current modal or dialog, doing nothing if none is open.
func CloseModal() Instruction {
	return Instruction{value: struct {
		Type string `json:"type"`
	}{Type: "close_modal"}}
}

type urlInstruction struct {
	Type   string `json:"type"`
	URL    string `json:"url"`
	Target string `json:"target,omitempty"`
}

// URLOption selects where an external URL opens.
type URLOption func(*urlInstruction)

// InNewTab opens an external URL in a new tab, the shell's default.
func InNewTab(i *urlInstruction) { i.Target = "blank" }

// InCurrentTab opens an external URL in the current tab.
func InCurrentTab(i *urlInstruction) { i.Target = "self" }

// OpenURL opens an absolute external URL in a new tab unless InCurrentTab is set.
func OpenURL(url string, opts ...URLOption) Instruction {
	i := urlInstruction{Type: "open_url", URL: url}
	for _, opt := range opts {
		opt(&i)
	}

	return Instruction{value: i}
}
