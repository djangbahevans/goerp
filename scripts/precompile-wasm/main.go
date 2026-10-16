// Command precompile-wasm prepares native WASM code for race-enabled tests.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tetratelabs/wazero"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, input io.Reader) error {
	flags := flag.NewFlagSet("precompile-wasm", flag.ContinueOnError)
	cacheDir := flags.String("cache", "", "Wazero compilation cache directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *cacheDir == "" || flags.NArg() != 0 {
		return errors.New("usage: precompile-wasm -cache <directory> < module.wasm")
	}

	binary, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("read WASM binary: %w", err)
	}

	cache, err := wazero.NewCompilationCacheWithDir(*cacheDir)
	if err != nil {
		return fmt.Errorf("open compilation cache: %w", err)
	}
	defer cache.Close(ctx)

	// Cancellation checks must match the engine because they affect Wazero's cache key.
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithCompilationCache(cache).
		WithCloseOnContextDone(true))
	defer rt.Close(ctx)

	if _, err := rt.CompileModule(ctx, binary); err != nil {
		return fmt.Errorf("compile WASM binary: %w", err)
	}
	return nil
}
