// Package buildinfo identifies the running engine build for module compatibility checks.
package buildinfo

// Release binaries set Version through -ldflags -X; dev builds only check range syntax.
var Version = "dev"
