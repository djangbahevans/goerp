package module

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	htmltemplate "html/template"
	"maps"
	"path/filepath"
	"slices"
	texttemplate "text/template"
	"text/template/parse"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/sdk/go/notify"
)

func init() {
	registerCollector(notificationsCollector{})
}

type notificationsCollector struct{}

func (notificationsCollector) Key() string { return "notification_types" }

func (notificationsCollector) Kinds() []string { return []string{notify.KindNotification} }

func (notificationsCollector) Collect(d Declarations, info ModuleInfo) (any, error) {
	definitions, err := decodeDeclarations[notify.NotificationDeclaration](d, notify.KindNotification)
	if err != nil {
		return nil, err
	}

	out := make([]manifest.NotificationType, 0, len(definitions))
	seen := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		if seen[definition.Name] {
			return nil, fmt.Errorf("notification %q is declared more than once with notify.Define", definition.Name)
		}
		seen[definition.Name] = true

		if !slices.Contains(definition.AvailableChannels, notify.ChannelInApp) || !slices.Contains(definition.DefaultChannels, notify.ChannelInApp) {
			return nil, fmt.Errorf("notification %q: default and available channels must include in_app", definition.Name)
		}
		templates := maps.Clone(definition.Templates)
		if templates == nil {
			templates = map[string]string{}
		}
		for _, channel := range definition.DefaultChannels {
			if !slices.Contains(definition.AvailableChannels, channel) {
				return nil, fmt.Errorf("notification %q: default channel %q is not available", definition.Name, channel)
			}
		}
		for _, channel := range definition.AvailableChannels {
			ext := ""
			switch channel {
			case notify.ChannelInApp, notify.ChannelPush:
				ext = "json"
			case notify.ChannelEmail:
				ext = "html"
			case notify.ChannelSMS:
				ext = "txt"
			default:
				return nil, fmt.Errorf("notification %q: unknown channel %q", definition.Name, channel)
			}
			if templates[channel] == "" {
				templates[channel] = fmt.Sprintf("notifications/%s/%s.{locale}.%s", definition.Name, channel, ext)
			}
		}

		schema := maps.Clone(definition.DataSchema)
		maps.DeleteFunc(schema, func(_ string, typ string) bool {
			return !slices.Contains([]string{"string", "int", "float", "bool"}, typ)
		})
		entry := manifest.NotificationType{
			Name:              definition.Name,
			Label:             definition.Label,
			Description:       definition.Description,
			DefaultChannels:   definition.DefaultChannels,
			AvailableChannels: definition.AvailableChannels,
			DefaultPriority:   string(cmp.Or(definition.DefaultPriority, notify.Normal)),
			Templates:         templates,
			DataSchema:        schema,
		}

		loaded, err := notiftemplate.Load([]manifest.NotificationType{entry}, info.Dir)
		if err != nil {
			return nil, err
		}
		if _, err := loaded.Rows(info.Name); err != nil {
			return nil, err
		}
		files := loaded.Files()
		for _, name := range slices.Sorted(maps.Keys(files)) {
			if err := checkNotificationTemplate(name, files[name], definition); err != nil {
				return nil, err
			}
		}
		out = append(out, entry)
	}

	slices.SortFunc(out, func(a, b manifest.NotificationType) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

var notificationStandardVariables = []string{"TenantName", "TenantLogoURL", "UserName", "UserFirstName", "ActionURL", "UnsubscribeURL", "Year"}

func checkNotificationTemplate(name string, data []byte, definition notify.NotificationDeclaration) error {
	sources := []string{string(data)}
	if filepath.Ext(name) == ".json" {
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			return fmt.Errorf("template %s: decode JSON fields: %w", name, err)
		}
		sources = nil
		for _, key := range []string{"title", "body", "action_url", "icon", "subject"} {
			if source, ok := fields[key].(string); ok {
				sources = append(sources, source)
			}
		}
	}

	for _, source := range sources {
		trees := map[string]*parse.Tree{}
		var root *parse.Tree
		if filepath.Ext(name) == ".html" {
			template, err := htmltemplate.New(name).Parse(source)
			if err != nil {
				return fmt.Errorf("template %s: %w", name, err)
			}
			root = template.Tree
			for _, template := range template.Templates() {
				trees[template.Name()] = template.Tree
			}
		} else {
			template, err := texttemplate.New(name).Parse(source)
			if err != nil {
				return fmt.Errorf("template %s: %w", name, err)
			}
			root = template.Tree
			for _, template := range template.Templates() {
				trees[template.Name()] = template.Tree
			}
		}

		if !definition.DataStruct {
			continue
		}
		if root == nil {
			continue
		}
		checker := notificationTemplateChecker{
			fields:  definition.DataSchema,
			trees:   trees,
			visited: map[string]bool{},
		}
		if err := checker.check(root.Root); err != nil {
			return fmt.Errorf("template %s: %w", name, err)
		}
	}
	return nil
}

type notificationTemplateChecker struct {
	fields  map[string]string
	trees   map[string]*parse.Tree
	visited map[string]bool
}

func (c notificationTemplateChecker) check(node parse.Node) error {
	if node == nil {
		return nil
	}
	visit := func(nodes ...parse.Node) error {
		for _, child := range nodes {
			if err := c.check(child); err != nil {
				return err
			}
		}
		return nil
	}

	switch node := node.(type) {
	case *parse.ListNode:
		if node != nil {
			for _, child := range node.Nodes {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
	case *parse.ActionNode:
		return visit(node.Pipe)
	case *parse.PipeNode:
		if node == nil {
			return nil
		}
		for _, command := range node.Cmds {
			if err := visit(command); err != nil {
				return err
			}
		}
	case *parse.CommandNode:
		return visit(node.Args...)
	case *parse.FieldNode:
		name := node.Ident[0]
		if _, ok := c.fields[name]; !ok && !slices.Contains(notificationStandardVariables, name) {
			return fmt.Errorf("variable .%s is neither a field of the notification data nor an engine standard variable", name)
		}
	case *parse.ChainNode:
		return visit(node.Node)
	case *parse.IfNode:
		return visit(node.Pipe, node.List, node.ElseList)
	case *parse.RangeNode:
		// The iteration expression uses the outer dot; its blocks may rebind it.
		return visit(node.Pipe)
	case *parse.WithNode:
		return visit(node.Pipe)
	case *parse.TemplateNode:
		if err := visit(node.Pipe); err != nil {
			return err
		}
		if node.Pipe == nil || len(node.Pipe.Cmds) != 1 || len(node.Pipe.Cmds[0].Args) != 1 {
			return nil
		}
		if _, rootDot := node.Pipe.Cmds[0].Args[0].(*parse.DotNode); !rootDot || c.visited[node.Name] {
			return nil
		}
		// A named template inherits the data schema only when passed the root dot.
		c.visited[node.Name] = true
		if tree := c.trees[node.Name]; tree != nil {
			return visit(tree.Root)
		}
	}
	return nil
}
