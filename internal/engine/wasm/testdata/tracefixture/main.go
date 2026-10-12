// Command tracefixture exercises module spans through the WASI host boundary.
package main

import (
	"errors"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/cache"
	"github.com/djangbahevans/goerp/sdk/go/crypto"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/trace"
	"github.com/vmihailenco/msgpack/v5"
)

//go:wasmimport host.trace span_start
func spanStart(ptr, size uint32) uint64

//go:wasmimport host.trace span_end
func spanEnd(ptr, size uint32) uint64

//go:wasmimport host.trace span_attr
func spanAttr(ptr, size uint32) uint64

var previousID string

var lookup = cache.Define[string, string]("tracefixture", func(key string) string { return key }, cache.TTL(time.Minute))

type result struct {
	Codes []string `msgpack:"codes"`
}

func call(fn func(uint32, uint32) uint64, input any, output any) string {
	data, _ := msgpack.Marshal(input)
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	packed := fn(ptr, uint32(len(data)))
	engine.Deallocate(ptr, uint32(len(data)))
	respPtr, respLen := uint32(packed>>32), uint32(packed)
	defer engine.Deallocate(respPtr, respLen)

	var envelope abi.Envelope
	if err := msgpack.Unmarshal(engine.ReadMem(respPtr, respLen), &envelope); err != nil {
		return err.Error()
	}
	if !envelope.OK {
		return envelope.Error.Code
	}
	if output != nil {
		if err := msgpack.Unmarshal(envelope.Data, output); err != nil {
			return err.Error()
		}
	}
	return ""
}

func writeResult(r result) uint64 {
	data, _ := msgpack.Marshal(r)
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

func flow() uint64 {
	outer := trace.Start("business.outer", trace.String("order.id", "order-1"),
		trace.Int("order.total", 9007199254740993), trace.Float("order.rate", 1.5), trace.Bool("order.active", true))
	defer outer.End()
	inner := trace.Start("business.inner")
	crypto.VerifyHMACSHA256([]byte("key"), []byte("data"), []byte("invalid"))
	_, _, cacheErr := lookup.Lookup("denied")
	inner.SetAttr("inner.count", int32(7))
	inner.SetStatus(trace.StatusError, "calculation failed")
	inner.RecordError(errors.New("invalid total"))
	inner.End()
	inner.End()
	inner.SetAttr("ignored", true)
	outer.SetAttr("outer.result", "failed")
	outer.SetStatus(trace.StatusOK, "")

	var raw abi.TraceSpanStartOutput
	codes := []string{call(spanStart, abi.TraceSpanStartInput{Name: "raw"}, &raw)}
	codes = append(codes, call(spanAttr, abi.TraceSpanAttrInput{SpanID: raw.SpanID, Key: "raw.active", Value: true}, nil))
	codes = append(codes, call(spanEnd, abi.TraceSpanEndInput{SpanID: "unknown"}, nil))
	codes = append(codes, call(spanAttr, abi.TraceSpanAttrInput{SpanID: "unknown", Key: "x", Value: true}, nil))
	codes = append(codes, call(spanEnd, abi.TraceSpanEndInput{SpanID: raw.SpanID, Status: "ok"}, nil))
	codes = append(codes, call(spanEnd, abi.TraceSpanEndInput{SpanID: raw.SpanID}, nil))
	codes = append(codes, call(spanAttr, abi.TraceSpanAttrInput{SpanID: raw.SpanID, Key: "x", Value: true}, nil))
	if cacheErr != nil {
		codes = append(codes, cacheErr.Error())
	}

	call(spanStart, abi.TraceSpanStartInput{Name: "leaked"}, &raw)
	previousID = raw.SpanID
	return writeResult(result{Codes: codes})
}

//go:wasmexport run_flow
func runFlow() uint64 {
	return flow()
}

//go:wasmexport run_stale
func runStale() uint64 {
	return writeResult(result{Codes: []string{
		call(spanEnd, abi.TraceSpanEndInput{SpanID: previousID}, nil),
		call(spanAttr, abi.TraceSpanAttrInput{SpanID: previousID, Key: "x", Value: true}, nil),
	}})
}

//go:wasmexport handle_request
func handleRequest(ptr, size uint32) uint64 {
	mode := string(engine.ReadMem(ptr, size))
	switch mode {
	case "panic":
		trace.Start("panic.leaked")
		panic("handler failed")
	case "timeout":
		trace.Start("timeout.leaked")
		for {
		}
	}
	return flow()
}

//go:wasmexport handle_orm_compute
func handleCompute(ptr, size uint32) uint64 {
	trace.Start("compute.leaked")
	return writeResult(result{})
}

//go:wasmexport handle_event
func handleEvent(ptr, size uint32) uint32 {
	trace.Start("event.leaked")
	return 2
}

//go:wasmexport handle_job
func handleJob(ptr, size uint32) uint32 {
	trace.Start("job.leaked")
	return 1
}

//go:wasmexport handle_cron
func handleCron(ptr, size uint32) uint32 {
	trace.Start("cron.leaked")
	return 0
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
