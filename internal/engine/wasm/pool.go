package wasm

import (
	"cmp"
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
	wasmRuntime *Runtime
	cfg         PoolConfig

	tokens chan struct{}

	idle chan *ModuleInstance

	mu            sync.Mutex
	draining      bool
	stopReplenish context.CancelFunc
	replenishDone chan struct{}
	closeIdle     func()

	seq      atomic.Int64
	borrowed atomic.Int64
	created  atomic.Int64
	closed   atomic.Int64
	waitTime prometheus.Histogram
}

func (cfg PoolConfig) withDefaults() PoolConfig {
	cfg.WarmSize = cmp.Or(cfg.WarmSize, 4)
	cfg.MaxSize = cmp.Or(cfg.MaxSize, 16)
	cfg.BorrowTimeout = cmp.Or(cfg.BorrowTimeout, 5*time.Second)
	cfg.MaxMemoryPages = cmp.Or(cfg.MaxMemoryPages, 256)
	return cfg
}

func newPool(name string, compiled wazero.CompiledModule, rt *Runtime, cfg PoolConfig) (*InstancePool, context.Context) {
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
		waitTime:      rt.metrics.BorrowHistogram(name),
	}

	// A reload's retired-pool cleanup can overlap engine shutdown.
	p.closeIdle = sync.OnceFunc(func() {
		p.stopReplenish()
		<-p.replenishDone

		close(p.idle)
		for inst := range p.idle {
			_ = inst.module.CloseWithExitCode(context.Background(), 0)
			p.closed.Add(1)
			<-p.tokens
		}
	})

	return p, replenishCtx
}

func NewInstancePool(name string, compiled wazero.CompiledModule, rt *Runtime, cfg PoolConfig) *InstancePool {
	p, replenishCtx := newPool(name, compiled, rt, cfg)
	go p.replenishLoop(replenishCtx)
	return p
}

func (p *InstancePool) InUseCount() int64 {
	return p.borrowed.Load()
}

func (p *InstancePool) IdleCount() int {
	return len(p.idle)
}

// WarmSize includes the default applied when configuration supplies zero.
func (p *InstancePool) WarmSize() int {
	return p.cfg.WarmSize
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
		p.wasmRuntime.metrics.BorrowTimeout(p.moduleName)
		return nil, ErrPoolTimeout
	case <-time.After(p.cfg.BorrowTimeout):
		p.wasmRuntime.metrics.BorrowTimeout(p.moduleName)
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

	inst, err := newModuleInstance(ctx, modName, p.compiled, p.wasmRuntime)
	if err != nil {
		return nil, err
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
			inst, err := p.instantiate(ctx)
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

func (p *InstancePool) DrainAndClose(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	p.mu.Lock()
	p.draining = true
	p.mu.Unlock()

	p.closeIdle()

	tick := time.Tick(10 * time.Millisecond)
	for {
		if p.borrowed.Load() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick:
		}
	}
}
