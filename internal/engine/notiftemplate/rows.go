package notiftemplate

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Row is one notification_templates row's content (notification-system.md
// §3): the template for one (template key, channel, locale). Fields maps a
// column name to the raw Go template source stored in it; a column the
// variant does not define is absent.
type Row struct {
	TemplateKey string
	Channel     string
	Locale      string
	Fields      map[string]string
}

// The notification_templates content columns a Row's Fields are keyed by.
const (
	ColTitle     = "title_template"
	ColBody      = "body_template"
	ColActionURL = "action_url_template"
	ColIcon      = "icon"
	ColSubject   = "subject_template"
	ColHTML      = "html_template"
	ColText      = "text_template"
	ColSMS       = "sms_template"
	ColPushTitle = "push_title_template"
	ColPushBody  = "push_body_template"
)

// Columns lists every Fields key a Row can carry.
var Columns = []string{ColTitle, ColBody, ColActionURL, ColIcon, ColSubject, ColHTML, ColText, ColSMS, ColPushTitle, ColPushBody}

// ChannelColumns lists, for each channel, the columns its rows carry.
var ChannelColumns = map[string][]string{
	"in_app": {ColTitle, ColBody, ColActionURL, ColIcon},
	"email":  {ColSubject, ColHTML, ColText},
	"sms":    {ColSMS},
	"push":   {ColPushTitle, ColPushBody},
}

var (
	inAppColumns = map[string]string{"title": ColTitle, "body": ColBody, "action_url": ColActionURL, "icon": ColIcon}
	pushColumns  = map[string]string{"title": ColPushTitle, "body": ColPushBody}
)

type rowID struct{ key, channel, locale string }

// Rows returns the rows for every template mt discovered, keyed
// "{moduleName}.{type}": one per (type, channel, locale), in a stable
// order. An email row merges the html body with the subject and
// plain-text variants of the same locale. A JSON variant that cannot be
// split into fields before rendering is left out and reported in the
// returned error, alongside the rows that were built.
func (mt *ModuleTemplates) Rows(moduleName string) ([]Row, error) {
	if mt == nil {
		return nil, nil
	}
	rows := map[rowID]*Row{}
	row := func(typ, channel, locale string) *Row {
		id := rowID{moduleName + "." + typ, channel, locale}
		if rows[id] == nil {
			rows[id] = &Row{TemplateKey: id.key, Channel: channel, Locale: locale, Fields: map[string]string{}}
		}
		return rows[id]
	}

	var errs []error
	for _, name := range slices.Sorted(maps.Keys(mt.templates)) {
		typ, channel, _ := strings.Cut(name, ".")
		for locale, src := range mt.templates[name].Sources {
			var err error
			switch channel {
			case "in_app":
				err = addJSONFields(row(typ, channel, locale), src, inAppColumns)
			case "push":
				err = addJSONFields(row(typ, channel, locale), src, pushColumns)
			case "email":
				row(typ, channel, locale).Fields[ColHTML] = string(src)
			case ChannelEmailSubject:
				err = addJSONFields(row(typ, "email", locale), src, map[string]string{"subject": ColSubject})
			case ChannelEmailText:
				row(typ, "email", locale).Fields[ColText] = string(src)
			case "sms":
				row(typ, channel, locale).Fields[ColSMS] = string(src)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("%s.%s (%s): %w", typ, channel, locale, err))
			}
		}
	}

	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if len(r.Fields) > 0 {
			out = append(out, *r)
		}
	}
	slices.SortFunc(out, func(a, b Row) int {
		return strings.Compare(a.TemplateKey+"\x00"+a.Channel+"\x00"+a.Locale, b.TemplateKey+"\x00"+b.Channel+"\x00"+b.Locale)
	})
	return out, errors.Join(errs...)
}

// addJSONFields stores each string field of src, a JSON object, in the row
// column columns maps its name to.
func addJSONFields(r *Row, src []byte, columns map[string]string) error {
	var obj map[string]any
	if err := json.Unmarshal(src, &obj); err != nil {
		return fmt.Errorf("not a JSON object: %w", err)
	}
	for name, column := range columns {
		v, ok := obj[name]
		if !ok {
			continue
		}
		str, isString := v.(string)
		if !isString {
			return fmt.Errorf("field %q is not a string", name)
		}
		r.Fields[column] = str
	}
	return nil
}
