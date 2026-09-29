package notify

import (
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/registry"
)

// inAppContent is a rendered in_app template (notification-system.md §5
// "In-app template"): the notifications row's display fields.
type inAppContent struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	ActionURL string `json:"action_url"`
	Icon      string `json:"icon"`
}

// renderInApp renders moduleName's in_app template for notificationName
// (the type's name without its module prefix) in the closest of locale's
// variants. A type with no in_app template — every engine type, for now —
// gets its label as title and nothing else.
func renderInApp(snapshot *registry.RegistrySnapshot, moduleName, notificationName, label, locale string, vars map[string]any) (inAppContent, error) {
	if snapshot == nil {
		return inAppContent{Title: label}, nil
	}
	matched, tmpl, ok := snapshot.NotifTemplate(moduleName, notificationName, notifications.ChannelInApp, locale)
	if !ok {
		return inAppContent{Title: label}, nil
	}

	rendered, err := notiftemplate.Render(tmpl, matched, jsonEscapedStrings(vars))
	if err != nil {
		return inAppContent{}, fmt.Errorf("render in_app template: %w", err)
	}
	var content inAppContent
	if err := json.Unmarshal([]byte(rendered), &content); err != nil {
		return inAppContent{}, fmt.Errorf("rendered in_app template is not a JSON object: %w", err)
	}
	if content.Title == "" {
		content.Title = label
	}
	return content, nil
}

// jsonEscapedStrings returns vars with every string in it, however deeply
// nested in maps and slices, escaped for use inside a JSON string literal,
// since the in_app template is a JSON document rendered as text: a quote
// or backslash in the data would otherwise end the string it lands in.
func jsonEscapedStrings(vars map[string]any) map[string]any {
	escaped, _ := jsonEscaped(vars).(map[string]any)
	return escaped
}

func jsonEscaped(v any) any {
	switch v := v.(type) {
	case string:
		// json.Marshal rejects invalid UTF-8; with it replaced, it cannot fail.
		quoted, _ := json.Marshal(strings.ToValidUTF8(v, "�"))
		return string(quoted[1 : len(quoted)-1])
	case map[string]string:
		out := make(map[string]string, len(v))
		for k, e := range v {
			out[k] = jsonEscaped(e).(string)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = jsonEscaped(e)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = jsonEscaped(e)
		}
		return out
	case []string:
		out := make([]string, len(v))
		for i, e := range v {
			out[i] = jsonEscaped(e).(string)
		}
		return out
	default:
		return v
	}
}
