package main

import "github.com/djangbahevans/goerp/sdk/go/engine"

func init() {
	engine.SSE("/events", func(req *engine.Request) *engine.Response {
		event := engine.Event{ID: "next", Data: map[string]string{"after": req.LastEventID()}}
		if req.QueryParam("done") == "true" {
			return engine.EventsDone(event)
		}
		return engine.Events(event)
	})
}
