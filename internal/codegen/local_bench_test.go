package codegen

import (
	"os"
	"testing"

	internalmodule "github.com/djangbahevans/goerp/internal/module"
)

func BenchmarkReadExports(b *testing.B) {
	dir := copyExampleModule(b)
	result, err := internalmodule.BuildWasm(b.Context(), dir, false)
	if err != nil {
		b.Fatal(err)
	}

	binary, err := os.ReadFile(result.WasmPath)
	if err != nil {
		b.Fatal(err)
	}

	for b.Loop() {
		routes, schema, err := readExports(b.Context(), binary)
		if err != nil {
			b.Fatal(err)
		}
		if len(routes) == 0 || len(schema.Models) == 0 {
			b.Fatal("module exports contain no routes or models")
		}
	}
}
