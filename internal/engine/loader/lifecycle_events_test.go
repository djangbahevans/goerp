package loader

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func lifecycleModel(event *model.LifecycleEvent, backend model.ModelBackend) model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:          "contact",
		Backend:       backend,
		Fields:        []model.NamedField{{Name: "id", Def: model.UUID().PrimaryKey()}, {Name: "name", Def: model.Text()}},
		OnCreateEvent: event,
	}
}

func TestValidateLifecycleEvents(t *testing.T) {
	tests := []struct {
		name    string
		model   model.ModelDeclaration
		wantErr string
	}{
		{"all payload fields exist", lifecycleModel(&model.LifecycleEvent{Name: "contact.created", Fields: []model.LifecycleField{{Name: "id", Record: "id"}, {Name: "name", Record: "name"}}}, ""), ""},
		{"payload key differs from the record field it reads", lifecycleModel(&model.LifecycleEvent{Name: "contact.created", Fields: []model.LifecycleField{{Name: "contact_id", Record: "id"}}}, ""), ""},
		{"payload key reads a missing record field", lifecycleModel(&model.LifecycleEvent{Name: "contact.created", Fields: []model.LifecycleField{{Name: "contact_id", Record: "contact_id"}}}, ""), `reads "contact_id", which is not a field of the model`},
		{"no lifecycle event", lifecycleModel(nil, ""), ""},
		{"payload field names no record field", lifecycleModel(&model.LifecycleEvent{Name: "contact.created", Fields: []model.LifecycleField{{Name: "id", Record: "id"}, {Name: "nickname", Record: "nickname"}}}, ""), `payload field "nickname" reads "nickname", which is not a field of the model`},
		{"transient model", lifecycleModel(&model.LifecycleEvent{Name: "contact.created"}, model.BackendTransient), "no Postgres table"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLifecycleEvents([]model.ModelDeclaration{tc.model})
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("err = %v, want nil", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
