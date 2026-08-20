// Command testworker is a minimal stand-in for a real module's
// workflow-worker binary, used only by manager_test.go: it dials Temporal
// and polls a task queue derived from GOERP_WORKFLOW_WORKER_MODULE (the
// same env var Manager.spawn sets on every real workflow-worker process),
// long enough for the test to observe a registered poller and then send
// SIGTERM.
package main

import (
	"os"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func main() {
	hostPort := os.Getenv("GOERP_TEMPORAL_HOST_PORT")
	if hostPort == "" {
		hostPort = "127.0.0.1:7233"
	}

	c, err := client.Dial(client.Options{HostPort: hostPort, Namespace: "default"})
	if err != nil {
		os.Exit(1)
	}
	defer c.Close()

	taskQueue := "goerp:" + os.Getenv("GOERP_WORKFLOW_WORKER_MODULE")
	w := worker.New(c, taskQueue, worker.Options{})
	if err := w.Run(worker.InterruptCh()); err != nil {
		os.Exit(1)
	}
}
