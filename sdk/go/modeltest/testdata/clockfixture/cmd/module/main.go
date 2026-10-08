// Command module probes guest clocks, sleep and entropy across the WASI boundary.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/golang-jwt/jwt/v5"
	"github.com/vmihailenco/msgpack/v5"
)

var initializedAt = time.Now()

type sample struct {
	InitializedAt time.Time         `json:"initialized_at"`
	Before        time.Time         `json:"before"`
	After         time.Time         `json:"after"`
	RequestedAt   time.Time         `json:"requested_at"`
	ElapsedNS     int64             `json:"elapsed_ns"`
	Random        string            `json:"random"`
	LibraryValid  bool              `json:"library_valid"`
	HostTime      abi.TimeNowOutput `json:"host_time"`
}

func init() {
	engine.GET("/sample", func(req *engine.Request) *engine.Response {
		before := time.Now()
		time.Sleep(time.Duration(req.QueryParamInt("sleep_ms", 30)) * time.Millisecond)
		after := time.Now()

		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			panic(err)
		}

		claims := jwt.RegisteredClaims{
			NotBefore: jwt.NewNumericDate(before.Add(-time.Minute)),
			ExpiresAt: jwt.NewNumericDate(before.Add(time.Minute)),
		}

		return engine.OK(sample{
			InitializedAt: initializedAt,
			Before:        before,
			After:         after,
			RequestedAt:   req.RequestedAt,
			ElapsedNS:     time.Since(before).Nanoseconds(),
			Random:        hex.EncodeToString(random[:]),
			LibraryValid:  jwt.NewValidator().Validate(claims) == nil,
			HostTime:      readHostTime(),
		})
	}, engine.Timeout(time.Second))
}

//go:wasmimport host.time now
func hostTime(ptr, size uint32) uint64

func readHostTime() abi.TimeNowOutput {
	packed := hostTime(0, 0)
	ptr, size := uint32(packed>>32), uint32(packed)
	defer engine.Deallocate(ptr, size)

	var env abi.Envelope
	if err := msgpack.Unmarshal(engine.ReadMem(ptr, size), &env); err != nil {
		panic(err)
	}
	if !env.OK {
		panic(env.Error)
	}

	var out abi.TimeNowOutput
	if err := msgpack.Unmarshal(env.Data, &out); err != nil {
		panic(err)
	}

	return out
}

type statusRequest struct {
	ExpectedMS int64 `msgpack:"expected_ms"`
	SleepMS    int64 `msgpack:"sleep_ms"`
}

func checkInvocation(ptr, size uint32) int32 {
	var req statusRequest
	if err := msgpack.Unmarshal(engine.ReadMem(ptr, size), &req); err != nil {
		return 2
	}

	before := time.Now()
	time.Sleep(time.Duration(req.SleepMS) * time.Millisecond)
	if before.UnixMilli() != req.ExpectedMS || time.Now().UnixMilli() != req.ExpectedMS || readHostTime().UnixMs != req.ExpectedMS {
		return 2
	}

	return 0
}

//go:wasmexport handle_event
func handleEvent(ptr, size uint32) int32 { return checkInvocation(ptr, size) }

//go:wasmexport handle_job
func handleJob(ptr, size uint32) int32 { return checkInvocation(ptr, size) }

//go:wasmexport handle_cron
func handleCron(ptr, size uint32) int32 { return checkInvocation(ptr, size) }

//go:wasmexport get_routes
func getRoutes() uint64 { return engine.SerialiseRouteTable() }

//go:wasmexport get_model_declarations
func getModelDeclarations() uint64 { return engine.WriteModels(model.Schema{}) }

//go:wasmexport get_data_migrations
func getDataMigrations() uint64 { return engine.WriteDataMigrations(nil) }

//go:wasmexport handle_request
func handleRequest(ptr, size uint32) uint64 { return engine.DispatchRequest(ptr, size) }

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }

func main() {}
