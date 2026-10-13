// Command module exercises storage operations through the real host ABI.
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	"github.com/djangbahevans/goerp/sdk/go/model"
	fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/storagefixture"
	"github.com/djangbahevans/goerp/sdk/go/storage"
)

func init() {
	engine.HandleJob(fixture.Inspect, inspect)
	engine.GET("/metadata", func(req *engine.Request) *engine.Response {
		file, err := storage.Metadata(req.QueryParam("file_id"))
		if err != nil {
			return engine.BadRequest(err)
		}
		return engine.OK(file)
	})
	engine.GET("/url", func(req *engine.Request) *engine.Response {
		url, err := storage.SignedURL(req.QueryParam("file_id"), 0)
		if err != nil {
			return engine.BadRequest(err)
		}
		return engine.OK(map[string]string{"url": url})
	})
	engine.POST("/upload", func(req *engine.Request) *engine.Response {
		file, err := storage.Upload(storage.UploadInput{
			Filename:    "invoice.pdf",
			ContentType: "application/pdf",
			Data:        []byte("%PDF-1.7\nfixture invoice\n"),
			Opts:        storage.UploadOpts{Purpose: "reports", Public: true},
		})
		if err != nil {
			return engine.BadRequest(err)
		}
		return engine.OK(map[string]string{"file_id": file.FileID, "url": file.URL})
	})
	engine.POST("/delete", func(req *engine.Request) *engine.Response {
		if err := storage.Delete(req.QueryParam("file_id")); err != nil {
			return engine.BadRequest(err)
		}
		return engine.OK(nil)
	})
}

func inspect(ctx *engine.JobContext, payload fixture.Payload) error {
	file, err := storage.Metadata(payload.FileID)
	if err != nil {
		return jobs.PermanentError(err)
	}
	return ctx.SetResult(file)
}

//go:wasmexport get_routes
func getRoutes() uint64 { return engine.SerialiseRouteTable() }

//go:wasmexport get_model_declarations
func getModelDeclarations() uint64 { return engine.WriteModels(model.Schema{}) }

//go:wasmexport get_data_migrations
func getDataMigrations() uint64 { return engine.WriteDataMigrations(nil) }

//go:wasmexport handle_request
func handleRequest(ptr, size uint32) uint64 { return engine.DispatchRequest(ptr, size) }

//go:wasmexport handle_job
func handleJob(ptr, size uint32) uint32 { return engine.DispatchJob(ptr, size) }

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }

func main() {}
