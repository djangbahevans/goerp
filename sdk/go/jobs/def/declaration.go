package def

import (
	"cmp"
	"encoding"
	"encoding/json/v2"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/declare"
	"github.com/vmihailenco/msgpack/v5"
)

// Declaration kinds recorded into sdk/go/declare for the job_types and
// cron_jobs collectors of `goerp module generate`.
const (
	KindJob         = "job"
	KindJobHandler  = "job_handler"
	KindCron        = "cron"
	KindCronHandler = "cron_handler"
)

// JobDeclaration is a Define call as the manifest generator reads it.
// PayloadFields are the msgpack names of a struct payload's fields, which
// UniqueBy must name one of; PayloadStruct is false for a payload such as a
// map, whose keys are not known from its type.
type JobDeclaration struct {
	Name           string   `json:"name"`
	Label          string   `json:"label"`
	Description    string   `json:"description,omitzero"`
	Queue          string   `json:"queue,omitzero"`
	TimeoutSeconds int      `json:"timeout_seconds,omitzero"`
	MaxAttempts    int      `json:"max_attempts,omitzero"`
	Priority       int      `json:"priority,omitzero"`
	UniqueBy       string   `json:"unique_by,omitzero"`
	PayloadStruct  bool     `json:"payload_struct,omitzero"`
	PayloadFields  []string `json:"payload_fields,omitzero"`
}

// CronDeclaration is a DefineCron call as the manifest generator reads it,
// with the cron defaults for timeout and queue already applied.
type CronDeclaration struct {
	Name              string `json:"name"`
	Label             string `json:"label"`
	Schedule          string `json:"schedule"`
	Description       string `json:"description,omitzero"`
	DisabledByDefault bool   `json:"disabled_by_default,omitzero"`
	TimeoutSeconds    int    `json:"timeout_seconds"`
	Queue             string `json:"queue"`
}

// HandlerDeclaration is an engine.HandleJob or engine.HandleCron call: the
// definition's name and the SDK routing name of its handler.
type HandlerDeclaration struct {
	Name    string `json:"name"`
	Handler string `json:"handler"`
}

// lazyJobDeclaration inspects the payload type only when the registry is
// exported, which a running module never does.
type lazyJobDeclaration struct {
	JobDeclaration
	payload reflect.Type
}

func (l lazyJobDeclaration) MarshalJSON() ([]byte, error) {
	d := l.JobDeclaration
	d.PayloadStruct, d.PayloadFields = payloadFields(l.payload)
	return json.Marshal(d)
}

func declareJob[P any](name string, spec Spec) {
	declare.Add(KindJob, lazyJobDeclaration{
		Name:           name,
		Label:          spec.Label,
		Description:    spec.Description,
		Queue:          spec.Queue,
		TimeoutSeconds: int(spec.Timeout / time.Second),
		MaxAttempts:    spec.MaxAttempts,
		Priority:       spec.Priority,
		UniqueBy:       spec.UniqueBy,
		payload:        reflect.TypeFor[P](),
	})
}

func declareCron(name string, spec Spec) {
	declare.Add(KindCron, CronDeclaration{
		Name:              name,
		Label:             spec.Label,
		Schedule:          spec.Schedule,
		Description:       spec.Description,
		DisabledByDefault: spec.DisabledByDefault,
		TimeoutSeconds:    int(spec.Timeout / time.Second),
		Queue:             spec.Queue,
	})
}

// payloadFields lists the msgpack keys of a struct payload, and reports
// whether t is a struct at all.
func payloadFields(t reflect.Type) (isStruct bool, names []string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false, nil
	}
	return true, structKeys(t)
}

// structKeys follows vmihailenco/msgpack's field rules: a tag's name, else the
// Go name; "-" and unexported fields left out; and an embedded struct inlined
// unless it is tagged noinline, encodes itself, or would shadow a key.
func structKeys(t reflect.Type) []string {
	var names []string
	for f := range t.Fields() {
		tag, options, _ := strings.Cut(f.Tag.Get("msgpack"), ",")
		if tag == "-" || (!f.IsExported() && !f.Anonymous) {
			continue
		}

		if f.Anonymous && !slices.Contains(strings.Split(options, ","), "noinline") {
			if inner, ok := inlinedStruct(f.Type); ok {
				innerKeys := structKeys(inner)
				if !slices.ContainsFunc(innerKeys, func(k string) bool { return slices.Contains(names, k) }) {
					names = append(names, innerKeys...)
					continue
				}
			}
		}
		names = append(names, cmp.Or(tag, f.Name))
	}
	return names
}

var selfEncoding = []reflect.Type{
	reflect.TypeFor[msgpack.CustomEncoder](),
	reflect.TypeFor[msgpack.Marshaler](),
	reflect.TypeFor[encoding.BinaryMarshaler](),
	reflect.TypeFor[encoding.TextMarshaler](),
}

func inlinedStruct(t reflect.Type) (reflect.Type, bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, false
	}
	for _, i := range selfEncoding {
		if t.Implements(i) || reflect.PointerTo(t).Implements(i) {
			return nil, false
		}
	}
	return t, true
}
