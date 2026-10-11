package main

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

type Extremum struct{}

func (Extremum) ResourceName() string { return "testmodule.extremum" }

type ExtremumStatus string

func (report *flowReport) recordReadDenial(name string, err error) {
	hostErr, ok := errors.AsType[*abiv1.HostError](err)
	report.Steps = append(report.Steps, stepResult{Step: name, OK: ok && hostErr.Code == abiv1.ErrCodeFieldReadDenied})
}

func (report *flowReport) recordExtrema[V any](name string, field orm.Sortable[Extremum, V], cond orm.Condition[Extremum]) {
	for _, operation := range []string{"min", "max"} {
		var value *V
		var err error
		if operation == "min" {
			value, err = orm.Min(field, cond)
		} else {
			value, err = orm.Max(field, cond)
		}

		step := stepResult{Step: name + "_" + operation, OK: err == nil, Detail: "null"}
		if err != nil {
			step.Error = err.Error()
		} else if value != nil {
			step.Detail = fmt.Sprint(*value)
		}

		report.Steps = append(report.Steps, step)
	}
}

//go:wasmexport run_extrema
func runExtrema() uint64 {
	var report flowReport
	region := orm.NewStringField[Extremum]("region")
	integer := orm.NewOrderedField[Extremum, int32]("integer")
	bigint := orm.NewOrderedField[Extremum, int64]("bigint")
	float := orm.NewOrderedField[Extremum, float64]("float")
	decimal := orm.NewOrderedField[Extremum, string]("decimal")
	char := orm.NewStringField[Extremum]("char")
	text := orm.NewStringField[Extremum]("text")
	selection := orm.NewOrderedField[Extremum, ExtremumStatus]("selection")
	enum := orm.NewOrderedField[Extremum, ExtremumStatus]("enum")
	date := orm.NewTimeField[Extremum]("date")
	timestamp := orm.NewTimeField[Extremum]("timestamp")

	for _, group := range []string{"values", "empty", "nulls", "zero"} {
		cond := region.Eq(group)
		report.recordExtrema(group+"_integer", integer, cond)
		report.recordExtrema(group+"_bigint", bigint, cond)
		report.recordExtrema(group+"_float", float, cond)
		report.recordExtrema(group+"_decimal", decimal, cond)
		report.recordExtrema(group+"_char", char, cond)
		report.recordExtrema(group+"_text", text, cond)
		report.recordExtrema(group+"_selection", selection, cond)
		report.recordExtrema(group+"_enum", enum, cond)
		report.recordExtrema(group+"_date", date, cond)
		report.recordExtrema(group+"_timestamp", timestamp, cond)

		count, err := orm.Count(cond)
		report.Steps = append(report.Steps, stepResult{Step: group + "_count", OK: err == nil, Detail: strconv.FormatInt(count, 10)})
		sum, err := orm.Sum(integer, cond)
		report.Steps = append(report.Steps, stepResult{Step: group + "_sum", OK: err == nil, Detail: strconv.FormatFloat(sum, 'f', -1, 64)})
		average, err := orm.Avg(integer, cond)
		report.Steps = append(report.Steps, stepResult{Step: group + "_avg", OK: err == nil, Detail: strconv.FormatFloat(average, 'f', -1, 64)})
	}

	restricted := orm.NewStringField[Extremum]("restricted")
	_, err := orm.Min(restricted, orm.MatchAll[Extremum]())
	report.recordReadDenial("min_read_denied", err)
	_, err = orm.Max(restricted, orm.MatchAll[Extremum]())
	report.recordReadDenial("max_read_denied", err)

	var named *ExtremumStatus
	named, err = orm.Min(selection, region.Eq("values"))
	report.Steps = append(report.Steps, stepResult{Step: "named_type", OK: err == nil && named != nil && *named == ExtremumStatus("alpha")})

	var at *time.Time
	at, err = orm.Max(timestamp, region.Eq("values"))
	report.Steps = append(report.Steps, stepResult{Step: "time_type", OK: err == nil && at != nil && at.Equal(time.Date(2026, 10, 11, 16, 30, 0, 123456000, time.UTC))})

	return writeReport(report)
}
