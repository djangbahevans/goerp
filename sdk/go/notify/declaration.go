package notify

import (
	"encoding/json/v2"
	"reflect"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/declare"
)

// KindNotification is the declaration kind recorded into sdk/go/declare for
// the notification_types collector of `goerp module generate`.
const KindNotification = "notification"

// NotificationDeclaration is a Define call as the manifest generator reads it.
// DataStruct is false for data such as a map, whose variables are not known
// from its type; otherwise DataSchema maps each template variable of D to
// "string", "int", "float" or "bool", or "other" for any other type.
// Templates holds only the paths a Template option overrode.
type NotificationDeclaration struct {
	Name              string            `json:"name"`
	Label             string            `json:"label"`
	Description       string            `json:"description,omitzero"`
	DefaultChannels   []string          `json:"default_channels"`
	AvailableChannels []string          `json:"available_channels"`
	DefaultPriority   Priority          `json:"default_priority"`
	Templates         map[string]string `json:"templates,omitzero"`
	DataStruct        bool              `json:"data_struct,omitzero"`
	DataSchema        map[string]string `json:"data_schema,omitzero"`
}

// lazyNotificationDeclaration derives the data schema only when the registry
// is exported, which a running module never does.
type lazyNotificationDeclaration struct {
	NotificationDeclaration
	data reflect.Type
}

func (l lazyNotificationDeclaration) MarshalJSON() ([]byte, error) {
	l.DataSchema, l.DataStruct = dataSchema(l.data)
	return json.Marshal(l.NotificationDeclaration)
}

func declareNotification[D any](name string, d definition) {
	declare.Add(KindNotification, lazyNotificationDeclaration{
		Name:              name,
		Label:             d.label,
		Description:       d.description,
		DefaultChannels:   d.defaultChannels,
		AvailableChannels: d.availableChannels,
		DefaultPriority:   d.priority,
		Templates:         d.templates,
		data:              reflect.TypeFor[D](),
	})
}

// dataSchema lists the template variables of struct type t as encoding/json/v2
// names them: a json tag's name, else the Go name, with "-" and unexported
// fields left out and an untagged embedded struct inlined.
func dataSchema(t reflect.Type) (schema map[string]string, isStruct bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, false
	}
	schema = map[string]string{}
	addFields(schema, t)
	return schema, true
}

func addFields(schema map[string]string, t reflect.Type) {
	for f := range t.Fields() {
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		if f.Anonymous && tag == "" {
			if inner := f.Type; inner.Kind() == reflect.Struct {
				addFields(schema, inner)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		name := tag
		if name == "" {
			name = f.Name
		}
		schema[name] = schemaType(f.Type)
	}
}

func schemaType(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Bool:
		return "bool"
	}
	return "other"
}
