package orm

import (
	"reflect"
	"testing"
	"time"
)

var (
	_ Sortable[testModel, int32]                  = NewOrderedField[testModel, int32]("integer")
	_ Sortable[testModel, int64]                  = NewOrderedField[testModel, int64]("bigint")
	_ Sortable[testModel, float64]                = NewOrderedField[testModel, float64]("float")
	_ Sortable[testModel, string]                 = NewOrderedField[testModel, string]("decimal")
	_ Sortable[testModel, string]                 = NewStringField[testModel]("text")
	_ Sortable[testModel, valuesTestModelStatusT] = NewOrderedField[testModel, valuesTestModelStatusT]("status")
	_ Sortable[testModel, time.Time]              = NewTimeField[testModel]("timestamp")

	_ func(Sortable[testModel, int32], Condition[testModel]) (*int32, error)                                   = Min[testModel, int32]
	_ func(Sortable[testModel, int64], Condition[testModel]) (*int64, error)                                   = Max[testModel, int64]
	_ func(Sortable[testModel, string], Condition[testModel]) (*string, error)                                 = Min[testModel, string]
	_ func(Sortable[testModel, valuesTestModelStatusT], Condition[testModel]) (*valuesTestModelStatusT, error) = Max[testModel, valuesTestModelStatusT]
	_ func(Sortable[testModel, time.Time], Condition[testModel]) (*time.Time, error)                           = Min[testModel, time.Time]
)

func TestSortableExcludesUnsupportedDescriptors(t *testing.T) {
	checks := []struct {
		field    reflect.Type
		sortable reflect.Type
	}{
		{reflect.TypeFor[Field[testModel, bool]](), reflect.TypeFor[Sortable[testModel, bool]]()},
		{reflect.TypeFor[Field[testModel, string]](), reflect.TypeFor[Sortable[testModel, string]]()},
		{reflect.TypeFor[BytesField[testModel]](), reflect.TypeFor[Sortable[testModel, []byte]]()},
		{reflect.TypeFor[StringField[otherModel]](), reflect.TypeFor[Sortable[testModel, string]]()},
	}
	for _, check := range checks {
		if check.field.Implements(check.sortable) {
			t.Errorf("%s unexpectedly implements %s", check.field, check.sortable)
		}
	}
}
