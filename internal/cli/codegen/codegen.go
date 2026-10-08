// Package codegen implements the goerp codegen command (cli-reference.md
// §6).
package codegen

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/djangbahevans/goerp/internal/codegen"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

const defaultOutput = "frontend/src/api/generated.ts"

// enginePollInterval is how often --from-engine --watch re-reads
// /_meta/schema; a var so tests can shorten it.
var enginePollInterval = 2 * time.Second

// localDebounce coalesces the burst of file events one save produces.
const localDebounce = 300 * time.Millisecond

type options struct {
	dir        string
	local      bool
	fromEngine bool
	url        string
	token      string
	output     string
	watch      bool
	module     string
	timeout    time.Duration
}

func NewCmd() *cobra.Command {
	var o options

	cmd := &cobra.Command{
		Use:   "codegen [path]",
		Short: "Generate a module's typed TypeScript API client",
		Long: "Generate a module's TypeScript API client (frontend/src/api/generated.ts) from its routes and model\n" +
			"declarations, read from a local build (--local) or a running engine's /_meta/schema (--from-engine).\n" +
			"path is the module directory, default the current directory; --output is relative to it.",
		Args: clierr.WrapArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.dir = "."
			if len(args) > 0 {
				o.dir = args[0]
			}
			if o.local == o.fromEngine {
				return clierr.Usage(errors.New("exactly one of --local or --from-engine is required"))
			}
			if o.fromEngine && o.token == "" {
				return clierr.Usage(errors.New("--from-engine requires --token"))
			}
			if o.local && o.module != "" {
				return clierr.Usage(errors.New("--module applies only with --from-engine"))
			}
			if t, err := cmd.Flags().GetDuration("timeout"); err == nil {
				o.timeout = t
			}
			return run(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o)
		},
	}

	cmd.Flags().BoolVar(&o.local, "local", false, "Generate from a local build of this module only (no running engine)")
	cmd.Flags().BoolVar(&o.fromEngine, "from-engine", false, "Generate from a running engine's /_meta/schema (includes cross-module data)")
	cmd.Flags().StringVar(&o.url, "url", "http://localhost:8080", "Main application server URL, on the tenant's host (with --from-engine)")
	cmd.Flags().StringVar(&o.token, "token", "", "Per-tenant API key for the main application server (required with --from-engine)")
	cmd.Flags().StringVar(&o.output, "output", defaultOutput, "Output file path, relative to the module directory")
	cmd.Flags().BoolVar(&o.watch, "watch", false, "Regenerate on source (--local) or schema (--from-engine) changes")
	cmd.Flags().StringVar(&o.module, "module", "", "Module to generate (with --from-engine; default: manifest.json's name)")

	return cmd
}

func run(ctx context.Context, stdout, stderr io.Writer, o options) error {
	outPath := o.output
	if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(o.dir, outPath)
	}

	g := &runner{o: o, outPath: outPath, stdout: stdout, client: &http.Client{Timeout: o.timeout}}
	if o.fromEngine && o.module == "" {
		name, err := manifestName(o.dir)
		if err != nil {
			return fmt.Errorf("resolve module name (pass --module): %w", err)
		}
		g.o.module = name
	}

	if !o.watch {
		return g.once(ctx)
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	if o.local {
		return g.watchLocal(ctx, stderr)
	}
	return g.watchEngine(ctx, stderr)
}

type runner struct {
	o       options
	outPath string
	stdout  io.Writer
	client  *http.Client
}

// once generates the client and writes it if it changed.
func (g *runner) once(ctx context.Context) error {
	content, err := g.generate(ctx)
	if err != nil {
		return err
	}
	return g.write(content)
}

func (g *runner) generate(ctx context.Context) ([]byte, error) {
	var in *codegen.Input
	var err error
	if g.o.local {
		in, err = codegen.LoadLocal(ctx, g.o.dir)
	} else {
		var raw []byte
		raw, err = codegen.FetchSchema(ctx, g.client, g.o.url, g.o.token)
		if err != nil {
			return nil, err
		}
		in, err = codegen.InputFromSchema(raw, g.o.module)
	}
	if err != nil {
		return nil, err
	}
	if in.ActionRefs, err = codegen.ScanActionRefs(g.o.dir, g.outPath); err != nil {
		return nil, err
	}
	return codegen.Generate(in)
}

func (g *runner) write(content []byte) error {
	if cur, err := os.ReadFile(g.outPath); err == nil && bytes.Equal(cur, content) {
		_, err := fmt.Fprintf(g.stdout, "%s is up to date\n", g.outPath)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(g.outPath), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(g.outPath, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", g.outPath, err)
	}
	_, err := fmt.Fprintf(g.stdout, "wrote %s\n", g.outPath)
	return err
}

// watchLocal generates once, then again whenever a Go source, go.mod,
// go.sum, manifest.json or frontend .ts/.tsx file other than the output
// under the module directory changes, until ctx ends. A failed generation
// is reported and watching continues.
func (g *runner) watchLocal(ctx context.Context, stderr io.Writer) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("start file watcher: %w", err)
	}
	defer w.Close()
	if err := addWatchDirs(w, g.o.dir); err != nil {
		return err
	}
	outAbs, err := filepath.Abs(g.outPath)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}

	g.report(stderr, g.once(ctx))

	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Has(fsnotify.Create) {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() && !codegen.SkipDir(filepath.Base(ev.Name)) {
					_ = addWatchDirs(w, ev.Name)
				}
			}
			if triggersLocalRun(ev.Name, outAbs) {
				debounce = time.After(localDebounce)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintln(stderr, "watch:", err)
		case <-debounce:
			debounce = nil
			g.report(stderr, g.once(ctx))
		}
	}
}

// watchEngine generates once, then polls /_meta/schema and writes the
// client again whenever the generated output changes, until ctx ends. It
// compares generated output rather than response bodies: the engine
// doesn't encode the schema's maps in a fixed order. A failed poll or
// generation is reported once until the error changes, and polling
// continues.
func (g *runner) watchEngine(ctx context.Context, stderr io.Writer) error {
	var last []byte
	var lastErr string
	poll := func() {
		content, err := g.generate(ctx)
		if err == nil && !bytes.Equal(content, last) {
			err = g.write(content)
			if err == nil {
				last = content
			}
		}
		if msg := errString(err); msg != lastErr {
			g.report(stderr, err)
			lastErr = msg
		}
	}

	poll()
	tick := time.Tick(enginePollInterval)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
			poll()
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (g *runner) report(stderr io.Writer, err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(stderr, "Error:", err)
	}
}

// triggersLocalRun reports whether a change to path starts a --local
// --watch run. The output file is excluded, so a run's own write doesn't
// start another.
func triggersLocalRun(path, outAbs string) bool {
	name := filepath.Base(path)
	if codegen.IsSourceFile(name) {
		return true
	}
	if !codegen.IsFrontendSourceFile(name) {
		return false
	}
	abs, err := filepath.Abs(path)
	return err == nil && abs != outAbs
}

func addWatchDirs(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && codegen.SkipDir(d.Name()) {
			return filepath.SkipDir
		}
		if err := w.Add(path); err != nil {
			return fmt.Errorf("watch %s: %w", path, err)
		}
		return nil
	})
}

func manifestName(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", err
	}
	var mf struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &mf); err != nil {
		return "", fmt.Errorf("parse manifest.json: %w", err)
	}
	if mf.Name == "" {
		return "", errors.New("manifest.json has no name")
	}
	return mf.Name, nil
}
