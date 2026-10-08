package codegen

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/guestclock"
	internalmodule "github.com/djangbahevans/goerp/internal/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/vmihailenco/msgpack/v5"
)

// localManifest is the part of manifest.json a local run reads.
type localManifest struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Wasm  *bool  `json:"wasm"`
	Views []struct {
		Name          string       `json:"name"`
		Type          string       `json:"type"`
		Resource      string       `json:"resource"`
		FetchRoute    string       `json:"fetch_route"`
		CreateRoute   string       `json:"create_route"`
		UpdateRoute   string       `json:"update_route"`
		DeleteRoute   string       `json:"delete_route"`
		Actions       []ViewAction `json:"actions"`
		BulkActions   []ViewAction `json:"bulk_actions"`
		HeaderActions []ViewAction `json:"header_actions"`
	} `json:"views"`
}

func readLocalManifest(dir string) (*localManifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var mf localManifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if mf.Name == "" {
		return nil, errors.New("manifest is missing name")
	}
	return &mf, nil
}

// LoadLocal reads the module in dir from a local build: it builds
// module.wasm first if it's missing or older than any of the module's Go
// sources (the same build `goerp module build` runs), then reads the
// binary's get_routes and get_model_declarations exports in a sandbox
// (readExports) and its hand-authored views from manifest.json. A local
// run sees only this module, so no model.Extend fields and no other
// module's resources or actions.
func LoadLocal(ctx context.Context, dir string) (*Input, error) {
	mf, err := readLocalManifest(dir)
	if err != nil {
		return nil, err
	}
	if mf.Wasm != nil && !*mf.Wasm {
		return nil, fmt.Errorf("module %s declares wasm: false, so it has no routes or models to generate a client from", mf.Name)
	}

	wasmPath := filepath.Join(dir, "module.wasm")
	stale, err := wasmStale(dir, wasmPath)
	if err != nil {
		return nil, err
	}
	if stale {
		if _, err := internalmodule.BuildWasm(ctx, dir, false); err != nil {
			return nil, fmt.Errorf("build module: %w", err)
		}
	}

	binary, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("read module binary: %w", err)
	}
	routes, sch, err := readExports(ctx, binary)
	if err != nil {
		return nil, err
	}
	return localInput(mf, routes, sch), nil
}

// wasmStale reports whether wasmPath is missing or older than any Go
// source, go.mod or go.sum under dir.
func wasmStale(dir, wasmPath string) (bool, error) {
	info, err := os.Stat(wasmPath)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat module binary: %w", err)
	}
	newest, err := newestSource(dir)
	if err != nil {
		return false, err
	}
	return newest.After(info.ModTime()), nil
}

// newestSource is the latest modification time of the Go sources, go.mod,
// go.sum and manifest.json under dir, skipping hidden directories and
// node_modules.
func newestSource(dir string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && SkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !IsSourceFile(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("scan module sources: %w", err)
	}
	return newest, nil
}

// IsSourceFile reports whether a file with this name feeds a local
// codegen run.
func IsSourceFile(name string) bool {
	return strings.HasSuffix(name, ".go") || name == "go.mod" || name == "go.sum" || name == "manifest.json"
}

// IsFrontendSourceFile reports whether a file with this name may hold the
// useAction/callAction references a run checks (ScanActionRefs).
func IsFrontendSourceFile(name string) bool {
	ext := filepath.Ext(name)
	return ext == ".ts" || ext == ".tsx"
}

// SkipDir reports whether a directory with this name is left out of a
// local run's source scan: hidden directories and node_modules.
func SkipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

// readExports instantiates binary in a sandbox and returns its
// get_routes and get_model_declarations output. The sandbox is a fresh
// wazero runtime with WASI for the Go runtime's own needs and nothing
// else: no filesystem, network or environment, and every host ABI import
// stubbed with a function that traps if called. Both exports only marshal
// the module's own compile-time declarations, so neither calls one
// (cli-reference.md §6).
func readExports(ctx context.Context, binary []byte) ([]abiv1.RouteDeclaration, model.Schema, error) {
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
	defer rt.Close(ctx)

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return nil, model.Schema{}, fmt.Errorf("instantiate wasi: %w", err)
	}
	compiled, err := rt.CompileModule(ctx, binary)
	if err != nil {
		return nil, model.Schema{}, fmt.Errorf("compile module: %w", err)
	}
	if err := stubHostImports(ctx, rt, compiled); err != nil {
		return nil, model.Schema{}, err
	}

	// A Go wasip1 reactor exports _initialize, which runs its init()s —
	// where routes are registered — the way the engine instantiates it.
	cfg := guestclock.New(ctx, time.Now).Configure(wazero.NewModuleConfig()).WithName("module").WithStartFunctions("_start", "_initialize")
	mod, err := rt.InstantiateModule(ctx, compiled, cfg)
	if err != nil {
		return nil, model.Schema{}, fmt.Errorf("instantiate module: %w", err)
	}
	if initFn := mod.ExportedFunction("init"); initFn != nil {
		if _, err := initFn.Call(ctx); err != nil {
			return nil, model.Schema{}, fmt.Errorf("module init: %w", err)
		}
	}

	var routes []abiv1.RouteDeclaration
	if err := callExport(ctx, mod, "get_routes", &routes); err != nil {
		return nil, model.Schema{}, err
	}
	var sch model.Schema
	if err := callExport(ctx, mod, "get_model_declarations", &sch); err != nil {
		return nil, model.Schema{}, err
	}
	return routes, sch, nil
}

// stubHostImports registers a host module for every non-WASI module
// compiled imports from, exporting a trapping function per import with the
// import's own signature — enough to link, and nothing more.
func stubHostImports(ctx context.Context, rt wazero.Runtime, compiled wazero.CompiledModule) error {
	byModule := map[string][]api.FunctionDefinition{}
	var moduleNames []string
	for _, fd := range compiled.ImportedFunctions() {
		mod, _, _ := fd.Import()
		if mod == wasi_snapshot_preview1.ModuleName {
			continue
		}
		if _, ok := byModule[mod]; !ok {
			moduleNames = append(moduleNames, mod)
		}
		byModule[mod] = append(byModule[mod], fd)
	}
	slices.Sort(moduleNames)

	for _, mod := range moduleNames {
		b := rt.NewHostModuleBuilder(mod)
		for _, fd := range byModule[mod] {
			_, name, _ := fd.Import()
			fn := mod + "." + name
			b.NewFunctionBuilder().
				WithGoModuleFunction(api.GoModuleFunc(func(context.Context, api.Module, []uint64) {
					panic(fmt.Errorf("host function %s is not available to goerp codegen: get_routes and get_model_declarations must not call host functions", fn))
				}), fd.ParamTypes(), fd.ResultTypes()).
				Export(name)
		}
		if _, err := b.Instantiate(ctx); err != nil {
			return fmt.Errorf("stub host module %s: %w", mod, err)
		}
	}
	return nil
}

// callExport calls a no-argument export returning a packed (ptr<<32 | len)
// msgpack buffer and decodes it into v.
func callExport(ctx context.Context, mod api.Module, name string, v any) error {
	fn := mod.ExportedFunction(name)
	if fn == nil {
		return fmt.Errorf("module has no %s export", name)
	}
	res, err := fn.Call(ctx)
	if err != nil {
		return fmt.Errorf("call %s: %w", name, err)
	}
	if len(res) != 1 {
		return fmt.Errorf("%s returned %d values, want 1", name, len(res))
	}
	ptr, length := uint32(res[0]>>32), uint32(res[0])
	data, ok := mod.Memory().Read(ptr, length)
	if !ok {
		return fmt.Errorf("%s returned an out-of-bounds buffer (ptr=%d len=%d)", name, ptr, length)
	}
	if err := msgpack.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode %s output: %w", name, err)
	}
	return nil
}

// localInput converts a local build's declarations into an Input.
func localInput(mf *localManifest, decls []abiv1.RouteDeclaration, sch model.Schema) *Input {
	enumValues := map[string][]string{}
	for _, t := range sch.Types {
		enumValues[t.Name] = t.Values
	}

	in := &Input{Module: mf.Name, PathPrefix: "/" + mf.Name}
	if mf.Type == "connector" {
		in.PathPrefix = "/connectors/" + mf.Name
	}

	for _, md := range sch.Models {
		if md == nil {
			continue
		}
		m := Model{Name: md.QualifiedName(mf.Name), LabelPlural: md.LabelPlural}
		for _, op := range md.EnabledOps {
			m.Ops = append(m.Ops, op.Name)
		}
		for _, f := range md.Fields {
			def := f.Def
			field := Field{
				Name:       f.Name,
				Kind:       def.Kind.String(),
				Required:   def.IsRequired,
				PrimaryKey: def.IsPrimaryKey,
				HasDefault: def.DefaultExpr != nil,
				Readonly:   def.IsReadonly || def.IsComputed,
			}
			switch def.Kind {
			case model.KindSelection:
				field.Values = def.SelectionValues
			case model.KindEnum:
				field.Values = enumValues[def.EnumType]
			}
			m.Fields = append(m.Fields, field)
			// Workflow transitions are engine-native actions get_routes never
			// lists; added as routes they're in view validation's catalog
			// and the action-name clash check, as in a --from-engine run.
			for _, t := range def.WorkflowTransitions {
				in.Routes = append(in.Routes, Route{
					Method: "POST", Model: m.Name, Name: t.ActionName, CRUDAction: "workflow_transition", Scope: RecordScope,
				})
			}
		}
		in.Models = append(in.Models, m)
	}

	for _, d := range decls {
		r := Route{
			Method:         d.Method,
			Path:           d.Path,
			Model:          qualify(mf.Name, d.Model),
			Name:           d.Name,
			CRUDAction:     d.CRUDAction,
			Scope:          d.Scope,
			ResponseIsList: d.ResponseIsList,
			Streaming:      d.Streaming,
			Websocket:      d.Websocket,
			RequestType:    d.RequestType,
			ResponseType:   d.ResponseType,
		}
		if r.Name != "" {
			r.Scope = actionScope(r.Name, r.Scope)
		}
		in.Routes = append(in.Routes, r)
	}

	for _, v := range mf.Views {
		actions := slices.Concat(v.Actions, v.BulkActions, v.HeaderActions)
		in.Views = append(in.Views, View{
			Name:        v.Name,
			Type:        v.Type,
			Resource:    v.Resource,
			FetchRoute:  v.FetchRoute,
			CreateRoute: v.CreateRoute,
			UpdateRoute: v.UpdateRoute,
			DeleteRoute: v.DeleteRoute,
			Actions:     actions,
		})
	}

	in.Catalog = map[string]*CatalogModule{mf.Name: catalogFor(in.Models, in.Routes)}
	return in
}

// actionScope is the scope the engine derives an engine.DefineAction's path
// with: a reserved name's is fixed by its op (list, create, preview and
// pivot address the collection), and a custom action addresses one record
// unless it declares collection scope.
func actionScope(name, declared string) string {
	switch name {
	case "list", "create", "preview", "pivot":
		return CollectionScope
	case "get", "update", "delete":
		return RecordScope
	}
	if declared == CollectionScope {
		return CollectionScope
	}
	return RecordScope
}

// qualify prefixes a bare model name with module; an already-qualified
// name, or an empty one, is returned unchanged.
func qualify(module, name string) string {
	if name == "" || strings.Contains(name, ".") {
		return name
	}
	return module + "." + name
}
