package module

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch builds immediately and after source changes, reporting build errors without
// ending the session. Context cancellation stops watching and cancels an active build.
func Watch(ctx context.Context, dir string, stderr io.Writer, build func(context.Context) error) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create build watcher: %w", err)
	}
	defer func() { _ = w.Close() }()

	add := func(root string) error {
		return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				return nil
			}
			if path != root && ignoredWatchDir(entry.Name()) {
				return filepath.SkipDir
			}
			return w.Add(path)
		})
	}

	if err := add(dir); err != nil {
		return fmt.Errorf("watch module: %w", err)
	}

	run := func() {
		if err := build(ctx); err != nil && ctx.Err() == nil {
			_, _ = fmt.Fprintf(stderr, "build failed; keeping the last successful module: %v\n", err)
		}
	}

	run()

	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-w.Events:
			if !ok {
				return fmt.Errorf("build watcher closed unexpectedly")
			}
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() && !ignoredWatchDir(info.Name()) {
					if err := add(event.Name); err != nil {
						return fmt.Errorf("watch new module directory: %w", err)
					}
					timer.Reset(300 * time.Millisecond)
				}
			}
			if watchedModuleFile(event.Name) && event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) != 0 {
				timer.Reset(300 * time.Millisecond)
			}
		case <-timer.C:
			run()
		case err, ok := <-w.Errors:
			if !ok {
				return fmt.Errorf("build watcher error channel closed")
			}
			return fmt.Errorf("watch module changes: %w", err)
		}
	}
}

func ignoredWatchDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "build" || name == "dist"
}

func watchedModuleFile(path string) bool {
	name := filepath.Base(path)
	return filepath.Ext(path) == ".go" || name == "go.mod" || name == "go.sum" || name == "manifest.json" || filepath.Ext(path) == ".json"
}
