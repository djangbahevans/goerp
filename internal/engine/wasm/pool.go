package wasm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/tetratelabs/wazero"
)

var (
	ErrPoolDraining = errors.New("wasm: instance pool is draining")
	ErrPoolTimeout  = errors.New("wasm: borrow timed out")
)

type PoolConfig struct {
	WarmSize       int
	MaxSize        int
	BorrowTimeout  time.Duration
	MaxMemoryPages uint32
}

type InstancePool struct {
	moduleName  string
	compiled    wazero.CompiledModule
	wasmRuntime wazero.Runtime
	cfg         PoolConfig

	tokens chan struct{}

	idle chan *ModuleInstance

	mu            sync.Mutex
	draining      bool
	stopReplenish context.CancelFunc
	replenishDone chan struct{}

	seq      atomic.Int64
	borrowed atomic.Int64
	created  atomic.Int64
	closed   atomic.Int64
	waitTime prometheus.Histogram
}

func (cfg PoolConfig) withDefaults() PoolConfig {
	if cfg.WarmSize == 0 {
		cfg.WarmSize = 4
	}
	if cfg.MaxSize == 0 {
		cfg.MaxSize = 16
	}
	if cfg.BorrowTimeout == 0 {
		cfg.BorrowTimeout = 5 * time.Second
	}
	if cfg.MaxMemoryPages == 0 {
		cfg.MaxMemoryPages = 256
	}
	return cfg
}

// newPool builds the pool and wires stopReplenish/replenishDone, but does not
// start replenishLoop — the caller decides that. Split out from
// NewInstancePool so tests can construct a pool with no background
// goroutine racing them for tokens.
func newPool(name string, compiled wazero.CompiledModule, rt wazero.Runtime, cfg PoolConfig) (*InstancePool, context.Context) {
	cfg = cfg.withDefaults()

	replenishCtx, stopReplenish := context.WithCancel(context.Background())
	p := &InstancePool{
		moduleName:    name,
		compiled:      compiled,
		wasmRuntime:   rt,
		cfg:           cfg,
		tokens:        make(chan struct{}, cfg.MaxSize),
		idle:          make(chan *ModuleInstance, cfg.WarmSize),
		stopReplenish: stopReplenish,
		replenishDone: make(chan struct{}),
		waitTime: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wasm_instance_pool_borrow_wait_seconds",
			Help:        "Time Borrow spent waiting for an instance to become available.",
			ConstLabels: prometheus.Labels{"module": name},
		}),
	}

	return p, replenishCtx
}

func NewInstancePool(name string, compiled wazero.CompiledModule, rt wazero.Runtime, cfg PoolConfig) *InstancePool {
	p, replenishCtx := newPool(name, compiled, rt, cfg)
	go p.replenishLoop(replenishCtx)
	return p
}

func (p *InstancePool) Borrow(ctx context.Context) (*ModuleInstance, error) {
	start := time.Now()

	p.mu.Lock()
	draining := p.draining
	p.mu.Unlock()
	if draining {
		return nil, ErrPoolDraining
	}

	select {
	case inst, ok := <-p.idle:
		if !ok {
			return nil, ErrPoolDraining
		}
		return p.afterBorrow(inst, start), nil
	default:
	}

	select {
	case p.tokens <- struct{}{}:
		inst, err := p.instantiate(ctx)
		if err != nil {
			<-p.tokens
			return nil, err
		}
		return p.afterBorrow(inst, start), nil

	case inst, ok := <-p.idle:
		if !ok {
			return nil, ErrPoolDraining
		}
		return p.afterBorrow(inst, start), nil

	case <-ctx.Done():
		return nil, ErrPoolTimeout
	case <-time.After(p.cfg.BorrowTimeout):
		return nil, ErrPoolTimeout
	}
}

func (p *InstancePool) afterBorrow(inst *ModuleInstance, start time.Time) *ModuleInstance {
	inst.inUse.Store(true)
	p.waitTime.Observe(time.Since(start).Seconds())
	p.borrowed.Add(1)
	return inst
}

func (p *InstancePool) Return(inst *ModuleInstance) {
	defer p.borrowed.Add(-1)

	inst.inUse.Store(false)
	_ = inst.module.CloseWithExitCode(context.Background(), 0)
	p.closed.Add(1)
	<-p.tokens
}

func (p *InstancePool) instantiate(ctx context.Context) (*ModuleInstance, error) {
	seq := p.seq.Add(1)
	modName := fmt.Sprintf("%s-%d", p.moduleName, seq)
	mod, err := p.wasmRuntime.InstantiateModule(ctx, p.compiled, wazero.NewModuleConfig().WithName(modName))
	if err != nil {
		return nil, fmt.Errorf("instantiate %s: %w", p.moduleName, err)
	}

	inst := &ModuleInstance{
		module: mod,
		memory: mod.Memory(),
	}
	inst.allocate = mod.ExportedFunction("allocate")
	inst.deallocate = mod.ExportedFunction("deallocate")
	inst.handleRequest = mod.ExportedFunction("handle_request")
	inst.handleEvent = mod.ExportedFunction("handle_event")
	inst.handleJob = mod.ExportedFunction("handle_job")

	if initFn := mod.ExportedFunction("init"); initFn != nil {
		if _, err := initFn.Call(ctx); err != nil {
			_ = mod.CloseWithExitCode(context.Background(), 0)
			return nil, fmt.Errorf("init() for %s: %w", p.moduleName, err)
		}
	}

	p.created.Add(1)
	return inst, nil
}

func (p *InstancePool) replenishLoop(ctx context.Context) {
	defer close(p.replenishDone)
	for {
		select {
		case <-ctx.Done():
			return
		case p.tokens <- struct{}{}:
			inst, err := p.instantiate(context.Background())
			if err != nil {
				<-p.tokens
				time.Sleep(100 * time.Millisecond)
				continue
			}
			select {
			case p.idle <- inst:
			case <-ctx.Done():
				_ = inst.module.CloseWithExitCode(context.Background(), 0)
				p.closed.Add(1)
				<-p.tokens
				return
			}
		}
	}
}
