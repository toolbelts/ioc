package ioc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/rs/zerolog/log"
)

// registeredProvider 缓存提供者及其类型名，避免重复 reflect 调用。
type registeredProvider struct {
	name     string
	provider Provider
}

// deferredEntry 封装延迟提供者，sync.Once 保证只加载一次。
// name 在注册时缓存，避免加载时重复 reflect 调用。
type deferredEntry struct {
	name     string // 类型名，注册时缓存
	provider DeferrableProvider
	once     sync.Once
	err      error
}

// Application 包装 Container 并管理完整的提供者生命周期：
//
//	Register → Setup → (running) → Shutdown
type Application struct {
	*Container

	mu            sync.RWMutex
	providers     []registeredProvider      // 已注册（非延迟）的提供者
	providerNames map[string]bool           // 按类型名去重
	deferred      map[string]*deferredEntry // 抽象名 → 延迟条目（懒加载，加载后删除）
	deferredCount atomic.Int32              // 剩余延迟条目数，为 0 时 Make 跳过 deferred 逻辑
	setupDone     bool                      // SetupAll() 完成后为 true
	setupCtx      context.Context           // SetupAll 时保存，延迟 provider Setup 时使用
	shutdownCbs   []func()                  // 额外的关闭回调
}

// New 创建一个新的 Application 及空容器。
func New() *Application {
	app := &Application{
		Container:     NewContainer(),
		providerNames: make(map[string]bool),
		deferred:      make(map[string]*deferredEntry),
	}

	// 自绑定，使提供者可以解析应用本身
	app.Instance("app", app)

	return app
}

// ---------- 提供者注册 ----------

// Register 向应用添加一个或多个提供者。
// 延迟提供者会单独存储，在首次解析时按需加载。
func (a *Application) Register(providers ...Provider) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, p := range providers {
		name := reflect.TypeOf(p).String()
		if a.providerNames[name] {
			continue // 跳过重复注册
		}
		a.providerNames[name] = true

		// 检查是否为延迟提供者
		if dp, ok := p.(DeferrableProvider); ok {
			entry := &deferredEntry{name: name, provider: dp}
			provides := dp.Provides()
			for _, abstract := range provides {
				a.deferred[abstract] = entry
			}
			a.deferredCount.Add(int32(len(provides)))
			log.Info().Str("provider", name).Any("provides", provides).Msg("deferred")
			continue
		}

		// 立即注册
		p.Register(a)
		a.providers = append(a.providers, registeredProvider{name: name, provider: p})
		log.Info().Str("provider", name).Msg("registered")
	}
}

// ---------- 生命周期 ----------

// SetupAll 对所有已注册且实现了 Setupable 的提供者调用 Setup()。
// 必须在所有 Register() 调用完成后调用一次。
// ctx 用于控制 setup 阶段的超时或取消，每个 provider Setup 前会检查 ctx 状态。
// 持锁期间只读取状态，Setup 调用在锁外执行，允许 provider 在 Setup 中调用 app.Make。
func (a *Application) SetupAll(ctx context.Context) error {
	a.mu.Lock()
	if a.setupDone {
		a.mu.Unlock()
		return fmt.Errorf("ioc: SetupAll already called")
	}
	// 快照 providers，释放锁后遍历，避免 Setup 内调用 app.Make 时发生死锁
	providers := make([]registeredProvider, len(a.providers))
	copy(providers, a.providers)
	a.mu.Unlock()

	for _, rp := range providers {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("ioc: setup cancelled: %w", err)
		}
		if s, ok := rp.provider.(Setupable); ok {
			log.Ctx(ctx).Info().Str("provider", rp.name).Msg("setup")
			if err := s.Setup(ctx, a); err != nil {
				return fmt.Errorf("ioc: setup %s: %w", rp.name, err)
			}
		}
	}

	a.mu.Lock()
	a.setupDone = true
	// 去掉取消信号但保留 ctx 中的 value，防止延迟 provider Setup 时收到已取消的 ctx
	a.setupCtx = context.WithoutCancel(ctx)
	a.mu.Unlock()
	return nil
}

// ShutdownAll 以**逆序**对所有已注册且实现了 Shutdownable 的提供者调用 Shutdown()。
// 未实现 Shutdownable 的提供者会被静默跳过。
// 提供者关闭完成后，会调用通过 OnShutdown() 注册的回调。
// ctx 用于控制 shutdown 阶段的超时或取消，超时后跳过剩余 provider。
// 持锁期间只读取状态，Shutdown 调用在锁外执行，允许 provider 在 Shutdown 中调用 app.Make。
func (a *Application) ShutdownAll(ctx context.Context) error {
	// 快照 providers 和 shutdownCbs，释放锁后遍历，避免 Shutdown 内调用 app.Make 时发生死锁
	a.mu.RLock()
	providers := make([]registeredProvider, len(a.providers))
	copy(providers, a.providers)
	shutdownCbs := make([]func(), len(a.shutdownCbs))
	copy(shutdownCbs, a.shutdownCbs)
	a.mu.RUnlock()

	var errs []error
	cancelled := false

	// 逆序关闭
	for i := len(providers) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			// 统计剩余未执行的 Shutdownable provider 数量
			skipped := 0
			for j := i; j >= 0; j-- {
				if _, ok := providers[j].provider.(Shutdownable); ok {
					skipped++
				}
			}
			errs = append(errs, fmt.Errorf("ioc: shutdown cancelled, %d shutdownable provider(s) skipped: %w", skipped, err))
			cancelled = true
			break
		}
		rp := providers[i]
		if s, ok := rp.provider.(Shutdownable); ok {
			log.Ctx(ctx).Info().Str("provider", rp.name).Msg("shutdown")
			if err := s.Shutdown(ctx, a); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", rp.name, err))
			}
		}
	}

	// ctx 未取消时才执行额外的关闭回调，取消意味着跳过整个关闭流程
	if !cancelled {
		for _, cb := range shutdownCbs {
			cb()
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("ioc: shutdown errors: %v", errors.Join(errs...))
	}
	return nil
}

// OnShutdown 注册一个在 ShutdownAll 期间被调用的回调。
func (a *Application) OnShutdown(cb func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.shutdownCbs = append(a.shutdownCbs, cb)
}

// ---------- 延迟加载 ----------

// Make 覆盖 Container.Make 以支持延迟提供者。
//
// 零开销快速路径：atomic 读 deferredCount，为 0 时直接透传 Container.Make，无任何锁。
// 命中时：短暂持锁取出 entry 并清理 map，锁外通过 sync.Once 加载 provider，避免死锁。
func (a *Application) Make(abstract string) (any, error) {
	// 零开销快速路径：所有延迟提供者已加载完毕，直接走 Container
	if a.deferredCount.Load() > 0 {
		if err := a.loadDeferred(abstract); err != nil {
			return nil, err
		}
	}

	return a.Container.Make(abstract)
}

// loadDeferred 检查并加载延迟提供者（仅当 deferredCount > 0 时由 Make 调用）。
// entry 保留在 map 中直到 once.Do 完成，确保并发 goroutine 都能找到 entry 并阻塞等待。
func (a *Application) loadDeferred(abstract string) error {
	a.mu.RLock()
	entry, found := a.deferred[abstract]
	a.mu.RUnlock()

	if !found {
		return nil
	}

	// 锁外执行加载，sync.Once 保证只执行一次。
	// 并发到达的 goroutine 会阻塞在 once.Do 直到加载完成。
	entry.once.Do(func() {
		entry.provider.Register(a)

		a.mu.Lock()
		a.providers = append(a.providers, registeredProvider{name: entry.name, provider: entry.provider})
		setupDone := a.setupDone
		a.mu.Unlock()

		log.Ctx(a.setupCtx).Info().Str("provider", entry.name).Msg("loading deferred")

		// 如果已完成 setup 阶段，立即调用 Setup
		if setupDone {
			if s, ok := entry.provider.(Setupable); ok {
				a.mu.RLock()
				setupCtx := a.setupCtx
				a.mu.RUnlock()
				if setupCtx == nil {
					setupCtx = context.Background()
				}
				entry.err = s.Setup(setupCtx, a)
				if entry.err != nil {
					entry.err = fmt.Errorf("ioc: deferred setup %s: %w", entry.name, entry.err)
				}
			}
		}

		// 加载完成后从 map 移除并递减计数
		a.mu.Lock()
		provides := entry.provider.Provides()
		for _, abs := range provides {
			delete(a.deferred, abs)
		}
		a.mu.Unlock()
		a.deferredCount.Add(-int32(len(provides)))
	})

	return entry.err
}

// MustMake 覆盖 Container.MustMake 以走延迟感知路径。
func (a *Application) MustMake(abstract string) any {
	return must(a.Make(abstract))
}

// ---------- 信号感知运行 ----------

// Run 启动应用并在 SIGINT/SIGTERM 时确保优雅关闭。
//
// 当传入 fn 时，运行该函数（向后兼容）。
// 当不传 fn 时，自动收集所有实现了 Servable 的提供者，并发启动它们。
//
// ctx 用于控制整个生命周期，信号到达或 ctx 取消时 fn/Serve 收到的 context 会被取消。
// ShutdownAll 使用独立的 context.Background()，确保关闭流程不受 fn 取消影响。
// 如需 shutdown 超时，调用方应直接使用 SetupAll + ShutdownAll 组合。
func (a *Application) Run(ctx context.Context, fns ...func(ctx context.Context) error) error {
	if err := a.SetupAll(ctx); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(ctx,
		os.Interrupt, syscall.SIGTERM,
	)
	defer cancel()

	// 有 fn 时运行 fn（向后兼容），否则自动启动 Servable 提供者
	var runErr error
	if len(fns) > 0 && fns[0] != nil {
		runErr = fns[0](ctx)
	} else {
		runErr = a.serveAll(ctx)
	}

	// 无论运行是否出错，都执行关闭；使用独立 context 确保关闭不被跳过
	shutdownErr := a.ShutdownAll(context.Background())

	if runErr != nil {
		return runErr
	}
	return shutdownErr
}

// serveAll 收集所有实现了 Servable 的提供者，并发启动它们。
// 任一 Serve 返回 error 时立即返回，Run 随后会调用 ShutdownAll 清理其余服务。
// 无 Servable 提供者时阻塞等待 ctx 取消（等待信号优雅退出）。
func (a *Application) serveAll(ctx context.Context) error {
	a.mu.RLock()
	providers := make([]registeredProvider, len(a.providers))
	copy(providers, a.providers)
	a.mu.RUnlock()

	var servables []Servable
	for _, rp := range providers {
		if s, ok := rp.provider.(Servable); ok {
			servables = append(servables, s)
		}
	}

	if len(servables) == 0 {
		log.Info().Msg("no servable providers, waiting for signal")
		<-ctx.Done()
		return nil
	}

	log.Info().Int("count", len(servables)).Msg("starting servable providers")

	errCh := make(chan error, len(servables))
	for _, s := range servables {
		go func() { errCh <- s.Serve(ctx) }()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return nil
	}
}
