// Command authzcallerfixture is a real Go module compiled to wasip1
// WASM for internal/engine/wasm's own host.authz module-side caller
// test (goerp#418) — it calls host.authz.field_check, through the real
// sdk/go/authz package, against both a field with a declared
// FieldSecurityRule ("credit_limit") and one without ("name"), rather
// than a hand-assembled bytecode stand-in.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o authzcallerfixture.wasm .
package main

import (
	"errors"
	"strconv"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/authz"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/perm"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	widgetModel = "testmodule.widget"

	// Invoice records the host test seeds: ownInvoice belongs to the caller,
	// otherInvoice to someone else, missingInvoice does not exist.
	ownInvoice     = "11111111-1111-1111-1111-111111111111"
	otherInvoice   = "22222222-2222-2222-2222-222222222222"
	missingInvoice = "99999999-9999-9999-9999-999999999999"
)

type stepResult struct {
	Step    string `msgpack:"step"`
	OK      bool   `msgpack:"ok"`
	Allowed bool   `msgpack:"allowed"`
	// Text carries a step's string result, such as a row filter's SQL.
	Text string `msgpack:"text,omitempty"`
	// Forbidden is set when the step's error is an *authz.ForbiddenError.
	Forbidden bool   `msgpack:"forbidden,omitempty"`
	Error     string `msgpack:"error,omitempty"`
}

type flowReport struct {
	Steps []stepResult `msgpack:"steps"`
}

func writeReport(r flowReport) uint64 {
	data, err := msgpack.Marshal(r)
	if err != nil {
		data, _ = msgpack.Marshal(flowReport{Steps: []stepResult{{Step: "marshal_report", Error: err.Error()}}})
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

//go:wasmexport run_authz_flow
func runAuthzFlow() uint64 {
	var report flowReport
	record := func(step string, allowed bool, err error) {
		sr := stepResult{Step: step, OK: err == nil, Allowed: allowed}
		if err != nil {
			sr.Error = err.Error()
		}
		report.Steps = append(report.Steps, sr)
	}

	allowed, err := authz.FieldCheck(widgetModel, "credit_limit", authz.Read)
	record("restricted_field_read", allowed, err)

	allowed, err = authz.FieldCheck(widgetModel, "name", authz.Read)
	record("unrestricted_field_read", allowed, err)

	financialsRead := perm.Ref("contacts:contact:financials_read")
	bankingRead := perm.Ref("hr:employee:banking_read")
	undeclared := perm.Ref("nowhere:thing:read")

	allowed, err = authz.Check(financialsRead, "")
	record("check_financials", allowed, err)

	allowed, err = authz.Check(bankingRead, "")
	record("check_banking", allowed, err)

	allowed, err = authz.Check(undeclared, "")
	record("check_undeclared", allowed, err)

	err = authz.Require(financialsRead, "")
	record("require_financials", err == nil, err)

	invoiceRead := perm.Ref("testmodule:invoice:read")

	allowed, err = authz.Check(invoiceRead, ownInvoice)
	record("check_record_admitted", allowed, err)

	allowed, err = authz.Check(invoiceRead, otherInvoice)
	record("check_record_rejected", allowed, err)

	allowed, err = authz.Check(invoiceRead, missingInvoice)
	record("check_record_missing", allowed, err)

	roles, err := authz.UserRoles()
	var roleSummary []string
	for _, r := range roles {
		summary := r.Name
		if r.IsSystem {
			summary += "*"
		}
		roleSummary = append(roleSummary, summary+"="+strconv.Itoa(len(r.ID)))
	}
	report.Steps = append(report.Steps, stepResult{Step: "user_roles", OK: err == nil, Text: strings.Join(roleSummary, ","), Error: errString(err)})

	filter, err := authz.RowFilter("invoice", invoiceRead)
	report.Steps = append(report.Steps, stepResult{Step: "row_filter", OK: err == nil, Text: filter.SQL, Error: errString(err)})

	err = authz.Require(invoiceRead, otherInvoice)
	_, recordForbidden := errors.AsType[*authz.ForbiddenError](err)
	report.Steps = append(report.Steps, stepResult{Step: "require_record_rejected", OK: err != nil, Forbidden: recordForbidden, Error: errString(err)})

	err = authz.Require(bankingRead, "")
	_, forbidden := errors.AsType[*authz.ForbiddenError](err)
	report.Steps = append(report.Steps, stepResult{Step: "require_banking", OK: err != nil, Forbidden: forbidden, Error: errString(err)})

	return writeReport(report)
}

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	return engine.Allocate(size)
}

//go:wasmexport deallocate
func deallocate(ptr, size uint32) {
	engine.Deallocate(ptr, size)
}

func main() {}
