package wasm

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestLogCallerFixture(t *testing.T) {
	wasmPath := filepath.Join(t.TempDir(), "logcaller.wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/logcallerfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build log fixture: %v\n%s", err, out)
	}
	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}

	r := newHostcallTestRuntime(t, nil, 10)
	compiled, err := r.wazero.CompileModule(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	inst, err := newModuleInstance(t.Context(), "sdk-log-caller", compiled, r)
	if err != nil {
		t.Fatal(err)
	}
	inst.SetModuleContext(NewModuleContext("req", "sales", "", "", nil, nil, "tenant", "", "trace", 0, nil, ModuleSnapshot{}))
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	for _, level := range []zerolog.Level{zerolog.InfoLevel, zerolog.DebugLevel} {
		var output bytes.Buffer
		r.moduleLogger = zerolog.New(&output).Level(level)
		if _, err := inst.module.ExportedFunction("run_logs").Call(t.Context()); err != nil {
			t.Fatal(err)
		}

		lines := strings.Split(strings.TrimSpace(output.String()), "\n")
		want := 3
		if level == zerolog.DebugLevel {
			want = 4
		}
		if len(lines) != want {
			t.Fatalf("level=%s: got %d lines, want %d: %s", level, len(lines), want, output.String())
		}
		for _, line := range lines {
			var got struct {
				Level     string  `json:"level"`
				Count     int64   `json:"count"`
				Success   bool    `json:"success"`
				Rate      float64 `json:"rate"`
				Module    string  `json:"module"`
				Password  string  `json:"password"`
				Duplicate string  `json:"duplicate"`
				Error     string  `json:"error"`
			}
			if err := json.Unmarshal([]byte(line), &got); err != nil {
				t.Fatal(err)
			}
			if got.Module != "sales" || strings.Contains(line, "fixture-secret") || strings.Contains(line, "fixture-token") || strings.Contains(line, "ignored") || strings.Contains(line, "unpaired") {
				t.Fatalf("incorrect SDK output: %s", line)
			}
			if got.Level == "info" && (got.Count != 9007199254740993 || !got.Success || got.Rate != 0.95 || got.Password != logRedacted || got.Duplicate != "last") {
				t.Fatalf("incorrect SDK fields: %s", line)
			}
			if got.Level == "error" && got.Error != "request failed: "+logRedacted {
				t.Fatalf("incorrect error value: %s", line)
			}
		}
	}
}
