// Package storagefixture defines a job that inspects a seeded file through storage calls.
package storagefixture

import jobdef "github.com/djangbahevans/goerp/sdk/go/jobs/def"

type Payload struct {
	FileID string `msgpack:"file_id"`
}

var Inspect = jobdef.Define[Payload]("inspect_file", jobdef.Label("Inspect file"))
