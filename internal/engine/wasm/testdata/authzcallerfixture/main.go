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
	"github.com/djangbahevans/goerp/sdk/go/authz"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	widgetModel = "testmodule.widget"
	callerUser  = "user-1"
)

type stepResult struct {
	Step    string `msgpack:"step"`
	OK      bool   `msgpack:"ok"`
	Allowed bool   `msgpack:"allowed"`
	Error   string `msgpack:"error,omitempty"`
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

	allowed, err := authz.FieldCheck(callerUser, widgetModel, "credit_limit", authz.Read)
	record("restricted_field_read", allowed, err)

	allowed, err = authz.FieldCheck(callerUser, widgetModel, "name", authz.Read)
	record("unrestricted_field_read", allowed, err)

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
