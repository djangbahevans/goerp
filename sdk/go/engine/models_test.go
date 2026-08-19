package engine

import (
	"reflect"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestWriteModels_PackingConvention(t *testing.T) {
	want := model.Schema{
		Types: []model.TypeDeclaration{
			{Name: "status", Values: []string{"draft", "active"}},
		},
		Models: []*model.ModelDeclaration{
			{Name: "widgets.widget"},
		},
	}

	packed := WriteModels(want)
	ptr := uint32(packed >> 32)
	length := uint32(packed)

	data := ReadMem(ptr, length)

	var got model.Schema
	if err := unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestWriteDataMigrations_PackingConvention(t *testing.T) {
	want := []model.DataMigration{
		{FromVersion: "0.9.0", ToVersion: "1.0.0", Handler: "migrate_0_9_to_1_0"},
	}

	packed := WriteDataMigrations(want)
	ptr := uint32(packed >> 32)
	length := uint32(packed)

	data := ReadMem(ptr, length)

	var got []model.DataMigration
	if err := unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
