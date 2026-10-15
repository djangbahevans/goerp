package module

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	internalmodule "github.com/djangbahevans/goerp/internal/module"
)

type devWriter struct {
	mu     *sync.Mutex
	writer io.Writer
}

func (w devWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(data)
}

func watchDevSources(ctx context.Context, cancel context.CancelCauseFunc, sources []devSource, repo, cliPath, sessionDir, modulesDir, storageDir string, client *adminclient.Client, stdout, stderr io.Writer) func() {
	ctx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup

	for _, source := range sources {
		ready := make(chan struct{})
		wg.Go(func() {
			first := true
			err := internalmodule.Watch(ctx, source.dir, stderr, func(ctx context.Context) error {
				// Startup packages are already built and loaded before watchers start.
				if first {
					first = false
					close(ready)
					return nil
				}

				build := func(output string) error {
					buildCtx, stopBuild := context.WithCancelCause(ctx)
					processes := &devProcesses{ctx: buildCtx, cancel: stopBuild, stdout: stdout, stderr: stderr}
					defer processes.stop()
					return processes.run(repo, nil, stdout, cliPath, source.buildArgs(output, storageDir, true)...)
				}
				reload := func(data []byte) error {
					_, err := client.PostBinary(ctx, "/admin/dev/modules/"+source.manifest.Name+"/reload", data)
					return err
				}

				return rebuildDevSource(source, sessionDir, modulesDir, build, reload)
			})
			if first {
				close(ready)
			}
			if err != nil && ctx.Err() == nil {
				cancel(fmt.Errorf("watch module %s: %w", source.manifest.Name, err))
			}
		})
		<-ready
	}

	return func() {
		stop()
		wg.Wait()
	}
}

func rebuildDevSource(source devSource, sessionDir, modulesDir string, build func(string) error, reload func([]byte) error) error {
	if err := source.checkTopology(); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(sessionDir, "rebuild-"+source.manifest.Name+"-")
	if err != nil {
		return fmt.Errorf("create rebuild directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	filename := source.manifest.Name + ".erp"
	candidate := filepath.Join(dir, filename)
	if err := build(candidate); err != nil {
		return fmt.Errorf("build %s: %w", source.manifest.Name, err)
	}
	if err := source.checkTopology(); err != nil {
		return err
	}
	data, err := os.ReadFile(candidate)
	if err != nil {
		return fmt.Errorf("read rebuilt package: %w", err)
	}
	if err := reload(data); err != nil {
		return fmt.Errorf("reload %s: %w", source.manifest.Name, err)
	}
	if err := os.Rename(candidate, filepath.Join(modulesDir, filename)); err != nil {
		return fmt.Errorf("publish accepted package: %w", err)
	}
	if err := os.Rename(candidate+".sha256", filepath.Join(modulesDir, filename+".sha256")); err != nil {
		return fmt.Errorf("publish accepted package checksum: %w", err)
	}
	return nil
}
