// Package manifest loads and validates a module's manifest.json:
// LoadManifest guards encoding (UTF-8, no comments — JSON syntax rejects
// those for free — and the 1MB size cap, manifest-spec.md §1), and Manifest
// is the typed struct every root field (manifest-spec.md §2) decodes into.
// Load only ever decodes raw bytes — extracting those bytes from wherever
// they live (a loose manifest.json fixture, or a real .erp package) is the
// caller's job, see internal/engine/moduleboot.Discover.
package manifest

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"unicode/utf8"
)

var (
	ErrInvalidUtf8      = errors.New("not valid utf-8")
	ErrInvalidJSON      = errors.New("not valid json")
	ErrManifestTooLarge = errors.New("manifest over 1mb size limit")
)

const (
	_  = iota
	KB = 1 << (10 * iota)
	MB
)

func Load(m []byte) (*Manifest, error) {
	if ok := utf8.Valid(m); !ok {
		return nil, ErrInvalidUtf8
	}

	// Unlike v1's json.Valid, this also rejects a duplicate object member
	// name — intentional, matching this migration's stricter decode paths.
	if ok := jsontext.Value(m).IsValid(); !ok {
		return nil, ErrInvalidJSON
	}

	if len(m) >= MB {
		return nil, ErrManifestTooLarge
	}

	var manifest Manifest
	if err := json.Unmarshal(m, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	if err := validateManifest(manifest); err != nil {
		return nil, fmt.Errorf("validate manifest: %w", err)
	}

	return &manifest, nil
}

type Manifest struct {
	Name                     string                `json:"name" validate:"required,name_regex"`
	DisplayName              string                `json:"display_name" validate:"required,max=128"`
	Type                     string                `json:"type" validate:"required"`
	Version                  string                `json:"version" validate:"required,semver"`
	Description              string                `json:"description" validate:"required,max=256"`
	LongDescription          string                `json:"long_description,omitempty" validate:"max_warn=4096,max=8192"`
	ABIVersion               string                `json:"abi_version" validate:"required,abi_version"`
	Engine                   string                `json:"engine" validate:"required,version_range"`
	Author                   string                `json:"author,omitempty"`
	License                  string                `json:"license,omitempty"`
	Homepage                 string                `json:"homepage,omitempty"`
	Repository               string                `json:"repository,omitempty"`
	Keywords                 []string              `json:"keywords,omitempty" validate:"max=10,dive,max=32"`
	DependsOn                []string              `json:"depends_on" validate:"required"`
	SoftDependsOn            []string              `json:"soft_depends_on,omitempty"`
	ConflictsWith            []string              `json:"conflicts_with,omitempty"`
	Capabilities             []string              `json:"capabilities" validate:"required"`
	HTTPAllowlist            []string              `json:"http_allowlist,omitempty"`
	Wasm                     bool                  `json:"wasm,omitzero"`
	Emits                    []EventDeclaration    `json:"emits,omitempty" validate:"dive"`
	Subscribes               []EventSubscription   `json:"subscribes,omitempty" validate:"dive"`
	Permissions              []Permission          `json:"permissions,omitempty"`
	Policies                 []Policy              `json:"policies,omitempty"`
	Views                    []View                `json:"views,omitempty"`
	ViewExtensions           []ViewExtensionRef    `json:"view_extensions,omitempty"`
	ViewExtensionDefinitions []ViewExtensionDef    `json:"view_extension_definitions,omitempty"`
	Navigation               []NavGroup            `json:"navigation,omitempty"`
	SearchIndexes            []SearchIndex         `json:"search_indexes,omitempty"`
	Reports                  []Report              `json:"reports,omitempty"`
	ReportOverrides          []ReportOverride      `json:"report_overrides,omitempty"`
	AnalyticsProjections     []AnalyticsProjection `json:"analytics_projections,omitempty"`
	JobTypes                 []JobType             `json:"job_types,omitempty"`
	WorkflowTypes            []WorkflowType        `json:"workflow_types,omitempty"`
	NotificationTypes        []NotificationType    `json:"notification_types,omitempty"`
	CronJobs                 []CronJob             `json:"cron_jobs,omitempty"`
	ConfigSchema             []ConfigEntry         `json:"config_schema,omitempty"`
	TenantConfigSeeds        map[string]any        `json:"tenant_config_seeds,omitempty"`
	AuditedTables            []AuditedTable        `json:"audited_tables,omitempty"`
	ErrorCodes               []string              `json:"error_codes,omitempty"`
	RetentionPolicies        []RetentionPolicy     `json:"retention_policies,omitempty"`
	Hooks                    []Hook                `json:"hooks,omitempty"`
	L10n                     L10nConfig            `json:"l10n"`
	Provides                 map[string]bool       `json:"provides,omitempty"`
	OAuthProvider            *OAuthProviderConfig  `json:"oauth_provider,omitempty"`
	WebhookAdapters          []string              `json:"webhook_adapters,omitempty"`
	Schema                   SchemaConfig          `json:"schema" validate:"required"`
	Frontend                 *FrontendConfig       `json:"frontend"`
	Checksum                 string                `json:"checksum" validate:"required"`
	WorkerChecksum           string                `json:"worker_checksum,omitempty"`
}

// UnmarshalJSON defaults Wasm to true (manifest-spec.md §2's documented
// default) when the manifest omits the field — plain json.Unmarshal into a
// bool zero-values it to false, which would misreport every module that
// relies on the documented default as having no WASM binary.
func (m *Manifest) UnmarshalJSON(data []byte) error {
	type Alias Manifest

	aux := &Alias{Wasm: true}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	*m = Manifest(*aux)

	return nil
}

type EventDeclaration struct {
	Name                string         `json:"name" validate:"required,event_name,max=128"`
	Version             int            `json:"version"`
	Description         string         `json:"description,omitempty"`
	PayloadSchema       map[string]any `json:"payload_schema,omitempty"`
	IdempotencyKeyField string         `json:"idempotency_key_field,omitempty"`
}

type EventSubscription struct {
	Name                string       `json:"name"`
	Version             int          `json:"version,omitzero"`
	Handler             string       `json:"handler,omitempty"`
	Async               bool         `json:"async"`
	IdempotencyKeyField string       `json:"idempotency_key_field,omitempty"`
	RetryPolicy         *RetryPolicy `json:"retry_policy,omitempty"`
}

type RetryPolicy struct {
	MaxAttempts    int    `json:"max_attempts" validate:"required,min=1,max=25"`
	Backoff        string `json:"backoff" validate:"required,oneof=none linear exponential"`
	InitialDelayMS int    `json:"initial_delay_ms" validate:"required,min=100"`
	MaxDelayMS     int    `json:"max_delay_ms,omitzero"`
	// Jitter is a pointer so an omitted field is distinguishable from an
	// explicit false — manifest-spec.md's documented default is true.
	Jitter *bool `json:"jitter,omitempty"`
}

type Permission struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Category     string   `json:"category,omitempty"`
	DefaultRoles []string `json:"default_roles,omitempty"`
}

type Policy struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	AppliesTo   string `json:"applies_to"`
	Condition   string `json:"condition"`
}

type View struct {
	Name              string         `json:"name"`
	Type              string         `json:"type"`
	Resource          string         `json:"resource"`
	Label             string         `json:"label"`
	Icon              string         `json:"icon,omitempty"`
	Permission        string         `json:"permission,omitempty"`
	Columns           []ListColumn   `json:"columns,omitempty"`
	DefaultSort       string         `json:"default_sort,omitempty"`
	DefaultSortDir    string         `json:"default_sort_dir,omitempty"`
	DefaultFilters    map[string]any `json:"default_filters,omitempty"`
	LabelField        string         `json:"label_field,omitempty"`
	RowClick          string         `json:"row_click,omitempty"`
	RowClickParam     string         `json:"row_click_param,omitempty"`
	Selectable        *bool          `json:"selectable,omitempty"`
	Filters           []Filter       `json:"filters,omitempty"`
	Actions           []Action       `json:"actions,omitempty"`
	BulkActions       []BulkAction   `json:"bulk_actions,omitempty"`
	GroupByOptions    []string       `json:"group_by_options,omitempty"`
	PageSizes         []int          `json:"page_sizes,omitempty"`
	DefaultPageSize   int            `json:"default_page_size,omitzero"`
	Density           string         `json:"density,omitempty"`
	EmptyState        *EmptyState    `json:"empty_state,omitempty"`
	CreateRoute       string         `json:"create_route,omitempty"`
	UpdateRoute       string         `json:"update_route,omitempty"`
	DeleteRoute       string         `json:"delete_route,omitempty"`
	FetchRoute        string         `json:"fetch_route,omitempty"`
	Sections          []FormSection  `json:"sections,omitempty"`
	Tabs              []FormTab      `json:"tabs,omitempty"`
	HeaderActions     []Action       `json:"header_actions,omitempty"`
	Sidebar           *FormSidebar   `json:"sidebar,omitempty"`
	Chatter           *bool          `json:"chatter,omitempty"`
	Autosave          bool           `json:"autosave,omitzero"`
	ReadonlyCondition string         `json:"readonly_condition,omitempty"`

	// Extra holds the members the engine does not model (the type-specific
	// members of kanban, calendar, timeline, pivot and custom views), kept
	// verbatim so /_meta/schema serves them to the shell unchanged.
	Extra map[string]jsontext.Value `json:"-"`
}

type ViewExtensionRef struct {
	Extends   string `json:"extends"`
	Extension string `json:"extension"`
}

type ViewExtensionDef struct {
	Name          string       `json:"name"`
	Type          string       `json:"type"`
	TargetSection string       `json:"target_section,omitempty"`
	Position      string       `json:"position,omitempty"`
	Tab           *FormTab     `json:"tab,omitempty"`
	Section       *FormSection `json:"section,omitempty"`
	Fields        []FormField  `json:"fields,omitempty"`
	Columns       []ListColumn `json:"columns,omitempty"`
	Filter        *Filter      `json:"filter,omitempty"`
	Action        *Action      `json:"action,omitempty"`
	BulkAction    *BulkAction  `json:"bulk_action,omitempty"`
}

type ListColumn struct {
	Field              string       `json:"field"`
	Label              string       `json:"label,omitempty"`
	Type               string       `json:"type,omitempty"`
	Sortable           bool         `json:"sortable,omitzero"`
	Width              int          `json:"width,omitzero"`
	MinWidth           int          `json:"min_width,omitzero"`
	MaxWidth           int          `json:"max_width,omitzero"`
	Truncate           *bool        `json:"truncate,omitempty"`
	Align              string       `json:"align,omitempty"`
	Primary            bool         `json:"primary,omitzero"`
	Hidden             bool         `json:"hidden,omitzero"`
	Href               string       `json:"href,omitempty"`
	Condition          string       `json:"condition,omitempty"`
	BadgeConfig        *BadgeConfig `json:"badge_config,omitempty"`
	AvatarField        string       `json:"avatar_field,omitempty"`
	Format             string       `json:"format,omitempty"`
	Resource           string       `json:"resource,omitempty"`
	DisplayField       string       `json:"display_field,omitempty"`
	ResourceLabelField string       `json:"resource_label_field,omitempty"`
	CurrencyField      string       `json:"currency_field,omitempty"`

	// Extra holds the members the engine does not model, kept verbatim.
	Extra map[string]jsontext.Value `json:"-"`
}

type BadgeConfig map[string]BadgeValue

type BadgeValue struct {
	Label string `json:"label"`
	Color string `json:"color,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

type Filter struct {
	Field          string         `json:"field"`
	Label          string         `json:"label,omitempty"`
	Type           string         `json:"type,omitempty"`
	Options        []FilterOption `json:"options,omitempty"`
	Default        any            `json:"default,omitempty"`
	Multiple       bool           `json:"multiple,omitzero"`
	Resource       string         `json:"resource,omitempty"`
	ResourceFilter map[string]any `json:"resource_filter,omitempty"`
	Condition      string         `json:"condition,omitempty"`
	SearchParams   string         `json:"search_params,omitempty"`
}

type FilterOption struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Icon     string `json:"icon,omitempty"`
	Color    string `json:"color,omitempty"`
	Disabled bool   `json:"disabled,omitzero"`
}

type Action struct {
	Label       string         `json:"label"`
	Type        string         `json:"type"`
	View        string         `json:"view,omitempty"`
	Icon        string         `json:"icon,omitempty"`
	Style       string         `json:"style,omitempty"`
	Permission  string         `json:"permission,omitempty"`
	Condition   string         `json:"condition,omitempty"`
	Route       string         `json:"route,omitempty"`
	RouteParams map[string]any `json:"route_params,omitempty"`
	Report      string         `json:"report,omitempty"`
	URL         string         `json:"url,omitempty"`
	Component   string         `json:"component,omitempty"`
	Confirm     *ConfirmDialog `json:"confirm,omitempty"`

	// Extra holds the members the engine does not model, kept verbatim.
	Extra map[string]jsontext.Value `json:"-"`
}

type ConfirmDialog struct {
	Title        string        `json:"title"`
	Message      string        `json:"message"`
	ConfirmLabel string        `json:"confirm_label,omitempty"`
	CancelLabel  string        `json:"cancel_label,omitempty"`
	Destructive  bool          `json:"destructive,omitzero"`
	Input        *ConfirmInput `json:"input,omitempty"`
}

type ConfirmInput struct {
	Field       string         `json:"field"`
	Label       string         `json:"label"`
	Type        string         `json:"type"`
	Required    bool           `json:"required,omitzero"`
	Placeholder string         `json:"placeholder,omitempty"`
	Options     []SelectOption `json:"options,omitempty"`
}

type SelectOption struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Icon     string `json:"icon,omitempty"`
	Color    string `json:"color,omitempty"`
	Disabled bool   `json:"disabled,omitzero"`
}

type BulkAction struct {
	Action
	MinSelected int `json:"min_selected,omitzero"`
	MaxSelected int `json:"max_selected,omitzero"`
}

type EmptyState struct {
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Icon        string            `json:"icon,omitempty"`
	Action      *EmptyStateAction `json:"action,omitempty"`
}

type EmptyStateAction struct {
	Label string `json:"label"`
	Type  string `json:"type"`
	View  string `json:"view,omitempty"`
}

type FormTab struct {
	Label            string         `json:"label,omitempty"`
	Icon             string         `json:"icon,omitempty"`
	Type             string         `json:"type,omitempty"`
	Field            string         `json:"field,omitempty"`
	InlineKey        string         `json:"inline_key,omitempty"`
	Columns          []ListColumn   `json:"columns,omitempty"`
	Permission       string         `json:"permission,omitempty"`
	Condition        string         `json:"condition,omitempty"`
	BadgeCountRoute  string         `json:"badge_count_route,omitempty"`
	View             string         `json:"view,omitempty"`
	Filter           map[string]any `json:"filter,omitempty"`
	ShowCreateAction bool           `json:"show_create_action,omitzero"`
	Sections         []FormSection  `json:"sections,omitempty"`
	Component        string         `json:"component,omitempty"`
}

type FormSection struct {
	Name               string      `json:"name,omitempty"`
	Label              string      `json:"label,omitempty"`
	Type               string      `json:"type,omitempty"`
	Columns            any         `json:"columns,omitempty"`
	Collapsible        bool        `json:"collapsible,omitzero"`
	CollapsedByDefault bool        `json:"collapsed_by_default,omitzero"`
	Condition          string      `json:"condition,omitempty"`
	Fields             []FormField `json:"fields,omitempty"`
	Field              string      `json:"field,omitempty"`
	InlineKey          string      `json:"inline_key,omitempty"`
	InlineEdit         bool        `json:"inline_edit,omitzero"`
	AddLabel           string      `json:"add_label,omitempty"`
	MaxRows            int         `json:"max_rows,omitzero"`
	Sort               string      `json:"sort,omitempty"`
	CreateRoute        string      `json:"create_route,omitempty"`
	UpdateRoute        string      `json:"update_route,omitempty"`
	DeleteRoute        string      `json:"delete_route,omitempty"`

	// Extra holds the members the engine does not model, kept verbatim.
	Extra map[string]jsontext.Value `json:"-"`
}

type FormField struct {
	Field              string         `json:"field"`
	Label              string         `json:"label,omitempty"`
	Type               string         `json:"type,omitempty"`
	Required           bool           `json:"required,omitzero"`
	Readonly           bool           `json:"readonly,omitzero"`
	ReadonlyCondition  string         `json:"readonly_condition,omitempty"`
	Hidden             bool           `json:"hidden,omitzero"`
	Condition          string         `json:"condition,omitempty"`
	Placeholder        string         `json:"placeholder,omitempty"`
	HelpText           string         `json:"help_text,omitempty"`
	Span               int            `json:"span,omitzero"`
	Autofocus          bool           `json:"autofocus,omitzero"`
	Computed           bool           `json:"computed,omitzero"`
	Options            []FieldOption  `json:"options,omitempty"`
	Resource           string         `json:"resource,omitempty"`
	ResourceFilter     map[string]any `json:"resource_filter,omitempty"`
	ResourceLabelField string         `json:"resource_label_field,omitempty"`
	Multiple           bool           `json:"multiple,omitzero"`
	Creatable          bool           `json:"creatable,omitzero"`
	Min                float64        `json:"min,omitzero"`
	Max                float64        `json:"max,omitzero"`
	Step               float64        `json:"step,omitzero"`
	Rows               int            `json:"rows,omitzero"`
	Accept             string         `json:"accept,omitempty"`
	MaxFileSizeMB      int            `json:"max_file_size_mb,omitzero"`
	CurrencyField      string         `json:"currency_field,omitempty"`
	Align              string         `json:"align,omitempty"`
	Component          string         `json:"component,omitempty"`
	ComponentProps     map[string]any `json:"component_props,omitempty"`
	Format             string         `json:"format,omitempty"`
	Suffix             string         `json:"suffix,omitempty"`
	Prefix             string         `json:"prefix,omitempty"`
	CopyToClipboard    bool           `json:"copy_to_clipboard,omitzero"`
	OpenInNewTab       *bool          `json:"open_in_new_tab,omitempty"`

	// Extra holds the members the engine does not model, kept verbatim.
	Extra map[string]jsontext.Value `json:"-"`
}

type FieldOption struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Icon     string `json:"icon,omitempty"`
	Color    string `json:"color,omitempty"`
	Disabled bool   `json:"disabled,omitzero"`
}

type FormSidebar struct {
	Width    int                  `json:"width,omitzero"`
	Sections []FormSidebarSection `json:"sections,omitempty"`
}

type FormSidebarSection struct {
	Label  string   `json:"label,omitempty"`
	Fields []string `json:"fields,omitempty"`
}

type NavGroup struct {
	Label      string    `json:"label"`
	Icon       string    `json:"icon,omitempty"`
	Order      int       `json:"order"`
	Permission string    `json:"permission,omitempty"`
	Condition  string    `json:"condition,omitempty"`
	Children   []NavItem `json:"children"`
}

type NavItem struct {
	Label           string         `json:"label"`
	Icon            string         `json:"icon,omitempty"`
	View            string         `json:"view,omitempty"`
	Route           string         `json:"route"`
	DefaultFilters  map[string]any `json:"default_filters,omitempty"`
	Permission      string         `json:"permission,omitempty"`
	BadgeCountRoute string         `json:"badge_count_route,omitempty"`
	Condition       string         `json:"condition,omitempty"`
	External        bool           `json:"external,omitzero"`
}

type SearchIndex struct {
	Name            string              `json:"name"`
	Resource        string              `json:"resource"`
	Table           string              `json:"table"`
	Searchable      []string            `json:"searchable"`
	Filterable      []string            `json:"filterable"`
	Sortable        []string            `json:"sortable,omitempty"`
	Displayed       []string            `json:"displayed"`
	Distinct        string              `json:"distinct,omitempty"`
	RankingRules    []string            `json:"ranking_rules,omitempty"`
	TypoTolerance   map[string]any      `json:"typo_tolerance,omitempty"`
	Synonyms        map[string][]string `json:"synonyms,omitempty"`
	StopWords       []string            `json:"stop_words,omitempty"`
	PrimaryKey      string              `json:"primary_key,omitempty"`
	SoftDeleteField string              `json:"soft_delete_field,omitempty"`
}

type Report struct {
	Name           string            `json:"name"`
	Label          string            `json:"label"`
	Description    string            `json:"description,omitempty"`
	Template       string            `json:"template"`
	HeaderTemplate string            `json:"header_template,omitempty"`
	FooterTemplate string            `json:"footer_template,omitempty"`
	Fonts          []string          `json:"fonts,omitempty"`
	DataHandler    string            `json:"data_handler"`
	Formats        []string          `json:"formats"`
	Permissions    []string          `json:"permissions"`
	Contexts       []string          `json:"contexts,omitempty"`
	Paper          string            `json:"paper,omitempty"`
	Orientation    string            `json:"orientation,omitempty"`
	MarginTopMM    int               `json:"margin_top_mm,omitzero"`
	MarginBottomMM int               `json:"margin_bottom_mm,omitzero"`
	MarginLeftMM   int               `json:"margin_left_mm,omitzero"`
	MarginRightMM  int               `json:"margin_right_mm,omitzero"`
	Parameters     []ReportParameter `json:"parameters,omitempty"`
	Scheduled      bool              `json:"scheduled,omitzero"`
	Batch          bool              `json:"batch,omitzero"`
	BatchLimit     int               `json:"batch_limit,omitzero"`
}

type ReportParameter struct {
	Name     string        `json:"name"`
	Label    string        `json:"label"`
	Type     string        `json:"type"`
	Required bool          `json:"required,omitzero"`
	Default  any           `json:"default,omitempty"`
	Options  []FieldOption `json:"options,omitempty"`
	Resource string        `json:"resource,omitempty"`
	Multiple bool          `json:"multiple,omitzero"`
	HelpText string        `json:"help_text,omitempty"`
}

type ReportOverride struct {
	Overrides   string   `json:"overrides"`
	Template    string   `json:"template"`
	DataHandler string   `json:"data_handler,omitempty"`
	Condition   string   `json:"condition,omitempty"`
	Description string   `json:"description,omitempty"`
	Formats     []string `json:"formats,omitempty"`
}

type AnalyticsProjection struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	SourceModel string   `json:"source_model"`
	Sync        string   `json:"sync"`
	Events      []string `json:"events,omitempty"`
	Description string   `json:"description,omitempty"`
}

type JobType struct {
	Name           string `json:"name"`
	Label          string `json:"label"`
	Handler        string `json:"handler"`
	Queue          string `json:"queue"`
	TimeoutSeconds int    `json:"timeout_seconds,omitzero"`
	MaxAttempts    int    `json:"max_attempts,omitzero"`
	UniqueBy       string `json:"unique_by,omitempty"`
	Description    string `json:"description,omitempty"`
	Priority       int    `json:"priority,omitzero"`
}

type WorkflowType struct {
	Name         string   `json:"name"`
	Label        string   `json:"label"`
	Description  string   `json:"description,omitempty"`
	InputModel   string   `json:"input_model,omitempty"`
	TimeoutHours int      `json:"timeout_hours,omitzero"`
	TaskQueue    string   `json:"task_queue,omitempty"`
	Activities   []string `json:"activities,omitempty"`
}

type NotificationType struct {
	Name              string            `json:"name"`
	Label             string            `json:"label"`
	Description       string            `json:"description,omitempty"`
	DefaultChannels   []string          `json:"default_channels"`
	AvailableChannels []string          `json:"available_channels"`
	DefaultPriority   string            `json:"default_priority,omitempty"`
	Templates         map[string]string `json:"templates,omitempty"`
	DataSchema        map[string]string `json:"data_schema,omitempty"`
}

type CronJob struct {
	Name             string `json:"name"`
	Label            string `json:"label"`
	Schedule         string `json:"schedule"`
	Handler          string `json:"handler"`
	Description      string `json:"description,omitempty"`
	EnabledByDefault *bool  `json:"enabled_by_default,omitempty"`
	TimeoutSeconds   int    `json:"timeout_seconds,omitzero"`
	Queue            string `json:"queue,omitempty"`
	PerTenant        *bool  `json:"per_tenant,omitempty"`
}

type ConfigEntry struct {
	Key             string        `json:"key"`
	Label           string        `json:"label"`
	Description     string        `json:"description,omitempty"`
	Type            string        `json:"type"`
	FieldType       string        `json:"field_type,omitempty"`
	Default         any           `json:"default"`
	Required        bool          `json:"required,omitzero"`
	Options         []FieldOption `json:"options,omitempty"`
	Min             float64       `json:"min,omitzero"`
	Max             float64       `json:"max,omitzero"`
	Category        string        `json:"category,omitempty"`
	Public          bool          `json:"public,omitzero"`
	RestartRequired bool          `json:"restart_required,omitzero"`
	ValidationRegex string        `json:"validation_regex,omitempty"`
	Encrypted       bool          `json:"encrypted,omitzero"`
	Generated       bool          `json:"generated,omitzero"`
}

type RetentionPolicy struct {
	Table        string `json:"table"`
	RetainDays   int    `json:"retain_days"`
	Action       string `json:"action"`
	Condition    string `json:"condition,omitempty"`
	ArchiveTable string `json:"archive_table,omitempty"`
	Description  string `json:"description,omitempty"`
	DryRunSafe   bool   `json:"dry_run_safe,omitzero"`
}

type Hook struct {
	Hook        string `json:"hook"`
	Handler     string `json:"handler"`
	Priority    int    `json:"priority,omitzero"`
	Description string `json:"description,omitempty"`
}

type L10nConfig struct {
	CountryCode            string       `json:"country_code,omitempty"`
	CountryName            string       `json:"country_name,omitempty"`
	CurrencyCode           string       `json:"currency_code,omitempty"`
	Languages              []string     `json:"languages,omitempty"`
	FiscalYearStartMonth   int          `json:"fiscal_year_start_month,omitzero"`
	FiscalYearStartDay     int          `json:"fiscal_year_start_day,omitzero"`
	TaxCalculationRounding string       `json:"tax_calculation_rounding,omitempty"`
	ChartOfAccounts        string       `json:"chart_of_accounts,omitempty"`
	SyscohadaCompatible    bool         `json:"syscohada_compatible,omitzero"`
	TinLabel               string       `json:"tin_label,omitempty"`
	TinRequired            bool         `json:"tin_required,omitzero"`
	TinFormat              string       `json:"tin_format,omitempty"`
	VatLabel               string       `json:"vat_label,omitempty"`
	InvoiceSequenceFormat  string       `json:"invoice_sequence_format,omitempty"`
	Provides               L10nProvides `json:"provides"`
}

type L10nProvides struct {
	ChartOfAccounts   bool `json:"chart_of_accounts,omitzero"`
	TaxConfiguration  bool `json:"tax_configuration,omitzero"`
	InvoiceFormat     bool `json:"invoice_format,omitzero"`
	PayrollRules      bool `json:"payroll_rules,omitzero"`
	BankConfiguration bool `json:"bank_configuration,omitzero"`
}

type SchemaConfig struct {
	OwnedModels       []string `json:"owned_models"`
	ExtendsModule     *string  `json:"extends_module,omitempty"`
	ExtendsModels     []string `json:"extends_models,omitempty"`
	HasDataMigrations bool     `json:"has_data_migrations,omitzero"`
}

type FrontendConfig struct {
	Bundle       *bool  `json:"bundle,omitempty" validate:"required"`
	Entry        string `json:"entry,omitempty"`
	BundleSHA256 string `json:"bundle_sha256,omitempty" validate:"required_if=Bundle true,omitempty,sha256_checksum"`
}

func (fc *FrontendConfig) UnmarshalJSON(data []byte) error {
	type Alias FrontendConfig

	aux := &Alias{
		Entry: "frontend/src/index.ts",
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	*fc = FrontendConfig(*aux)

	return nil
}

type OAuthProviderConfig struct {
	Name        string   `json:"name"`
	Label       string   `json:"label,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
	Issuer      string   `json:"issuer_url,omitempty"`
	AuthURL     string   `json:"auth_url,omitempty"`
	TokenURL    string   `json:"token_url,omitempty"`
	UserInfoURL string   `json:"userinfo_url,omitempty"`
}

type AuditedTable struct {
	Table          string
	ExcludeColumns []string
}

func (a *AuditedTable) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		a.Table = name
		a.ExcludeColumns = nil
		return nil
	}

	var obj struct {
		Table          string   `json:"table"`
		ExcludeColumns []string `json:"exclude_columns,omitempty"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	a.Table = obj.Table
	a.ExcludeColumns = obj.ExcludeColumns
	return nil
}

func (a AuditedTable) MarshalJSON() ([]byte, error) {
	if len(a.ExcludeColumns) == 0 {
		return json.Marshal(a.Table)
	}
	return json.Marshal(struct {
		Table          string   `json:"table"`
		ExcludeColumns []string `json:"exclude_columns,omitempty"`
	}{a.Table, a.ExcludeColumns})
}
