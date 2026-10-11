// Package asyncfixture declares typed event, job and config fixtures for harness tests.
package asyncfixture

import (
	"time"

	"github.com/djangbahevans/goerp/sdk/go/config/def"
	eventdef "github.com/djangbahevans/goerp/sdk/go/events/def"
	jobdef "github.com/djangbahevans/goerp/sdk/go/jobs/def"
)

type Payload struct {
	Reference string `msgpack:"reference"`
	Mode      string `msgpack:"mode"`
}

type Settings struct {
	Country string `json:"country"`
}

var (
	Received      = eventdef.Define[Payload]("asyncprobe.input.received", eventdef.Description("An input was received."))
	ReceivedV2    = eventdef.Define[Payload]("asyncprobe.input.received", eventdef.Version(2), eventdef.Description("An input was received."))
	Committed     = eventdef.Define[Payload]("asyncprobe.input.committed", eventdef.Description("An input is processed transactionally."))
	Process       = jobdef.Define[Payload]("process_input", jobdef.Label("Process input"), jobdef.Queue(jobdef.QueueBulk), jobdef.MaxAttempts(5))
	Country       = def.String("country", "GH", def.Label("Country"))
	Enabled       = def.Bool("enabled", true, def.Label("Enabled"))
	Count         = def.Int("count", 1, def.Label("Count"))
	Ratio         = def.Float("ratio", 0.5, def.Label("Ratio"))
	Interval      = def.Duration("interval", time.Minute, def.Label("Interval"))
	Countries     = def.StringSlice("countries", []string{"GH"}, def.Label("Countries"))
	Counts        = def.IntSlice("counts", []int{1}, def.Label("Counts"))
	Ratios        = def.FloatSlice("ratios", []float64{0.5}, def.Label("Ratios"))
	SettingsValue = def.JSON("settings", Settings{Country: "GH"}, def.Label("Settings"))
	Secret        = def.String("secret", "", def.Label("Secret"), def.Encrypted())
)
