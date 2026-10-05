package model

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/events/def"
)

// changedFieldsTag is the msgpack tag of the payload field an OnUpdate
// event may declare to receive the fields the write changed.
const changedFieldsTag = "changed_fields"

// LifecycleEvent is a record lifecycle event a model declares with
// OnCreate, OnUpdate or OnDelete. The engine builds the payload by
// selecting Fields from the written record, so the payload type is the
// field selection (go-sdk-reference.md §7 "Declaring emission on a model").
type LifecycleEvent struct {
	Name        string `msgpack:"name"`
	Version     int    `msgpack:"version"`
	Description string `msgpack:"description,omitempty"`
	// Fields are the payload fields drawn from the record, excluding
	// ChangedFields.
	Fields []LifecycleField `msgpack:"fields"`
	// ChangedFields is true when the payload declares a changed_fields
	// field for the engine to fill with the fields the write changed.
	ChangedFields bool `msgpack:"changed_fields,omitempty"`
}

// LifecycleField maps one payload field to the record field it reads.
type LifecycleField struct {
	// Name is the payload field's msgpack name, the key on the wire.
	Name string `msgpack:"name"`
	// Record is the model field the value is read from: the payload field's
	// `record` tag, or Name when it has none.
	Record string `msgpack:"record"`
}

// OnCreate declares the event emitted when a record is created, in the
// same transaction as the write. The definition's payload type must be a
// struct whose fields each name a field of the model, by msgpack tag or by
// a `record:"<field>"` tag when the wire key differs from the model field.
func (d *ModelDeclaration) OnCreate(event def.Definition) *ModelDeclaration {
	d.OnCreateEvent = lifecycleEvent("OnCreate", event, false)
	return d
}

// OnUpdate declares the event emitted when a record is updated. The
// payload may also declare ChangedFields []string with the msgpack tag
// changed_fields, which the engine fills with the fields the write changed.
func (d *ModelDeclaration) OnUpdate(event def.Definition) *ModelDeclaration {
	d.OnUpdateEvent = lifecycleEvent("OnUpdate", event, true)
	return d
}

// OnDelete declares the event emitted when a record is deleted. Its
// payload is built from the record as it was before the deletion.
func (d *ModelDeclaration) OnDelete(event def.Definition) *ModelDeclaration {
	d.OnDeleteEvent = lifecycleEvent("OnDelete", event, false)
	return d
}

func lifecycleEvent(modifier string, event def.Definition, allowChangedFields bool) *LifecycleEvent {
	t := event.PayloadType()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("model.%s: event %s payload type %s must be a struct", modifier, event.Name(), t))
	}

	le := &LifecycleEvent{
		Name: event.Name(), Version: event.Version(), Description: event.Description(),
		Fields: []LifecycleField{},
	}
	for field := range t.Fields() {
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("msgpack"), ",")
		switch name {
		case "-":
			continue
		case "":
			name = field.Name
		}
		if allowChangedFields && name == changedFieldsTag {
			if field.Type != reflect.TypeFor[[]string]() {
				panic(fmt.Sprintf("model.%s: event %s payload field %s must be []string", modifier, event.Name(), field.Name))
			}
			le.ChangedFields = true
			continue
		}
		source := name
		if tag := field.Tag.Get("record"); tag != "" {
			source = tag
		}
		le.Fields = append(le.Fields, LifecycleField{Name: name, Record: source})
	}
	slices.SortFunc(le.Fields, func(a, b LifecycleField) int { return strings.Compare(a.Name, b.Name) })
	return le
}
