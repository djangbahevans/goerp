package def

import (
	"encoding/json/v2"
	"reflect"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/declare"
	"github.com/djangbahevans/goerp/sdk/go/internal/payloadfields"
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
	fields, isStruct := payloadfields.Of(t)
	for _, f := range fields {
		names = append(names, f.Key)
	}
	return isStruct, names
}
