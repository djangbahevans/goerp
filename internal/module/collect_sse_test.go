package module

import (
	"fmt"
	"strings"
	"testing"
)

func TestGenerateRejectsInvalidSSEPollInterval(t *testing.T) {
	for _, interval := range []string{"499*time.Millisecond", "time.Minute+time.Nanosecond"} {
		main := fmt.Sprintf(`package main
import (
 "time"
 "github.com/djangbahevans/goerp/sdk/go/engine"
)
func init(){ engine.SSE("/events", func(*engine.Request)*engine.Response { return engine.Events() },engine.PollEvery(%s)) }
func main(){}
`, interval)
		dir := writeCollectFixture(t, main, `{"name":"widgets","version":"1.0.0","wasm":"module.wasm"}`)
		_, err := Generate(generateCtx(t), dir, GenerateOptions{Check: true})
		if err == nil || !strings.Contains(err.Error(), "poll interval must be between") {
			t.Fatalf("interval=%s error=%v", interval, err)
		}
	}
}
