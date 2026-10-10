// Command module sends typed notifications through each supported SDK send path.
package main

import (
	"errors"

	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
	fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/notificationsfixture"
	"github.com/djangbahevans/goerp/sdk/go/notify"
)

func init() {
	engine.POST("/send", func(req *engine.Request) *engine.Response {
		data := fixture.Data{
			Reference: req.QueryParam("reference"),
			Customer:  "Kwame Mensah",
			Amount:    9007199254740993,
		}
		opts := []notify.NotifyOption{notify.WithActionURL("/_m/notifprobe/orders/" + data.Reference)}
		if key := req.QueryParam("key"); key != "" {
			opts = append(opts, notify.WithIdempotencyKey(key))
		}

		var err error
		switch req.QueryParam("mode") {
		case "channels":
			opts = append(opts, notify.ForceChannel(notify.ChannelSMS), notify.AdditionalChannel(notify.ChannelPush))
			err = fixture.Confirmed.Send(req.UserID, data, opts...)
		case "tx", "rollback":
			err = db.WithTx(func(tx *db.Tx) error {
				if err := fixture.Confirmed.SendTx(tx, req.UserID, data, opts...); err != nil {
					return err
				}
				if req.QueryParam("mode") == "rollback" {
					return errors.New("roll back notification")
				}

				return nil
			})
		case "bulk":
			err = fixture.Confirmed.SendBulk([]string{req.UserID, req.QueryParam("recipient")}, data, opts...)
		default:
			err = fixture.Confirmed.Send(req.UserID, data, opts...)
		}
		if err != nil {
			return &engine.Response{
				StatusCode: 500,
				Body: map[string]any{
					"error": map[string]any{
						"code":    "notifprobe.send_failed",
						"message": err.Error(),
					},
				},
			}
		}

		return engine.OK(map[string]any{"sent": true})
	})
}

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
