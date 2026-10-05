package model

import (
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/events/def"
	"github.com/vmihailenco/msgpack/v5"
)

type contactCreatedPayload struct {
	ID       string `msgpack:"id"`
	Name     string `msgpack:"name"`
	Skipped  string `msgpack:"-"`
	Untagged string
	Email    string `msgpack:"email,omitempty"`
}

type contactUpdatedPayload struct {
	ID            string   `msgpack:"id"`
	ChangedFields []string `msgpack:"changed_fields"`
}

func TestLifecycleEvents_RecordDefinitionAndPayloadFields(t *testing.T) {
	created := def.Define[contactCreatedPayload]("contact.created", def.Version(2), def.Description("A contact was created"))
	updated := def.Define[contactUpdatedPayload]("contact.updated")
	deleted := def.Define[contactCreatedPayload]("contact.deleted")

	d := Define("contacts.contact").OnCreate(created).OnUpdate(updated).OnDelete(deleted)

	want := &LifecycleEvent{
		Name: "contact.created", Version: 2, Description: "A contact was created",
		Fields: []string{"Untagged", "email", "id", "name"},
	}
	if got := d.OnCreateEvent; got.Name != want.Name || got.Version != want.Version || got.Description != want.Description || !slices.Equal(got.Fields, want.Fields) || got.ChangedFields {
		t.Errorf("OnCreateEvent = %+v, want %+v", got, want)
	}
	if got := d.OnUpdateEvent; got.Version != 1 || !got.ChangedFields || !slices.Equal(got.Fields, []string{"id"}) {
		t.Errorf("OnUpdateEvent = %+v, want version 1, ChangedFields, Fields [id]", got)
	}
	if got := d.OnDeleteEvent; got.Name != "contact.deleted" {
		t.Errorf("OnDeleteEvent = %+v", got)
	}
}

func TestLifecycleEvents_ChangedFieldsOnlyHonouredOnUpdate(t *testing.T) {
	d := Define("contacts.contact").OnCreate(def.Define[contactUpdatedPayload]("contact.created"))

	if got := d.OnCreateEvent; got.ChangedFields || !slices.Contains(got.Fields, "changed_fields") {
		t.Errorf("OnCreateEvent = %+v, want changed_fields kept as an ordinary field", got)
	}
}

func TestLifecycleEvents_NonStructPayloadPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("OnCreate with a map payload did not panic")
		}
	}()
	Define("contacts.contact").OnCreate(def.Define[map[string]any]("contact.created"))
}

func TestLifecycleEvents_RoundTripThroughMsgpack(t *testing.T) {
	d := Define("contacts.contact").OnUpdate(def.Define[contactUpdatedPayload]("contact.updated"))

	data, err := msgpack.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ModelDeclaration
	if err := msgpack.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.OnUpdateEvent == nil || got.OnUpdateEvent.Name != "contact.updated" || !got.OnUpdateEvent.ChangedFields || got.OnCreateEvent != nil {
		t.Errorf("round-tripped declaration = %+v", got)
	}
}
