package ioc_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/toolbelts/ioc"
)

// ---------- 提供者 mock ----------

type mockProvider struct {
	registerCalled bool
	setupCalled    bool
	shutdownCalled bool
}

func (p *mockProvider) Register(_ *ioc.Application)                          { p.registerCalled = true }
func (p *mockProvider) Setup(_ context.Context, _ *ioc.Application) error    { p.setupCalled = true; return nil }
func (p *mockProvider) Shutdown(_ context.Context, _ *ioc.Application) error { p.shutdownCalled = true; return nil }

// ---------- 生命周期 ----------

func TestApp_Lifecycle(t *testing.T) {
	app := ioc.New()
	p := &mockProvider{}
	app.Register(p)

	if !p.registerCalled {
		t.Error("Register should be called immediately")
	}

	app.SetupAll(context.Background())
	if !p.setupCalled {
		t.Error("Setup should be called during SetupAll")
	}

	app.ShutdownAll(context.Background())
	if !p.shutdownCalled {
		t.Error("Shutdown should be called during ShutdownAll")
	}
}

func TestApp_SelfBinding(t *testing.T) {
	app := ioc.New()
	v, err := app.Make("app")
	if err != nil {
		t.Fatalf("app self-binding failed: %v", err)
	}
	if v != app {
		t.Error("self-binding should resolve to same Application")
	}
}

// ---------- Register ----------

func TestApp_Register_Dedup(t *testing.T) {
	app := ioc.New()
	count := 0
	p := &countingProvider{count: &count}
	app.Register(p)
	app.Register(p) // 重复注册

	if count != 1 {
		t.Errorf("duplicate provider should be skipped: Register called %d times", count)
	}
}

type countingProvider struct {
	count *int
}

func (p *countingProvider) Register(_ *ioc.Application) { *p.count++ }

type registerOnlyProvider struct{ called bool }

func (p *registerOnlyProvider) Register(_ *ioc.Application) { p.called = true }

func TestApp_SkipNoShutdown(t *testing.T) {
	app := ioc.New()
	p := &registerOnlyProvider{}
	app.Register(p)
	if err := app.SetupAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := app.ShutdownAll(context.Background()); err != nil {
		t.Fatal("should not error when provider has no Shutdown")
	}
}

// ---------- SetupAll ----------

func TestApp_SetupAll_CalledTwice(t *testing.T) {
	app := ioc.New()
	if err := app.SetupAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := app.SetupAll(context.Background())
	if err == nil {
		t.Error("SetupAll called twice should return error")
	}
}

func TestApp_SetupAll_Error(t *testing.T) {
	app := ioc.New()
	app.Register(&failingSetupProvider{})
	err := app.SetupAll(context.Background())
	if err == nil {
		t.Error("SetupAll should propagate Setup error")
	}
}

type failingSetupProvider struct{}

func (p *failingSetupProvider) Register(_ *ioc.Application) {}
func (p *failingSetupProvider) Setup(_ context.Context, _ *ioc.Application) error {
	return errors.New("setup failed")
}

// ---------- ShutdownAll ----------

func TestApp_ShutdownReverseOrder(t *testing.T) {
	app := ioc.New()
	var order []string

	p1 := &orderTrackerFirst{order: &order}
	p2 := &orderTrackerSecond{order: &order}

	app.Register(p1, p2)
	app.SetupAll(context.Background())
	app.ShutdownAll(context.Background())

	if len(order) != 2 || order[0] != "second" || order[1] != "first" {
		t.Errorf("shutdown should be in reverse order, got: %v", order)
	}
}

type orderTrackerFirst struct{ order *[]string }

func (p *orderTrackerFirst) Register(_ *ioc.Application) {}
func (p *orderTrackerFirst) Shutdown(_ context.Context, _ *ioc.Application) error {
	*p.order = append(*p.order, "first")
	return nil
}

type orderTrackerSecond struct{ order *[]string }

func (p *orderTrackerSecond) Register(_ *ioc.Application) {}
func (p *orderTrackerSecond) Shutdown(_ context.Context, _ *ioc.Application) error {
	*p.order = append(*p.order, "second")
	return nil
}

func TestApp_ShutdownAll_Error(t *testing.T) {
	app := ioc.New()
	app.Register(&failingShutdownProvider{})
	app.SetupAll(context.Background())
	err := app.ShutdownAll(context.Background())
	if err == nil {
		t.Error("ShutdownAll should propagate Shutdown error")
	}
}

type failingShutdownProvider struct{}

func (p *failingShutdownProvider) Register(_ *ioc.Application) {}
func (p *failingShutdownProvider) Shutdown(_ context.Context, _ *ioc.Application) error {
	return errors.New("shutdown failed")
}

// ---------- OnShutdown ----------

func TestApp_OnShutdown(t *testing.T) {
	app := ioc.New()
	called := false
	app.OnShutdown(func() { called = true })
	app.SetupAll(context.Background())
	app.ShutdownAll(context.Background())
	if !called {
		t.Error("OnShutdown callback not invoked")
	}
}

func TestApp_OnShutdown_Multiple(t *testing.T) {
	app := ioc.New()
	count := 0
	app.OnShutdown(func() { count++ })
	app.OnShutdown(func() { count++ })
	app.SetupAll(context.Background())
	app.ShutdownAll(context.Background())
	if count != 2 {
		t.Errorf("all shutdown callbacks should fire: got %d", count)
	}
}

// ---------- 延迟提供者 ----------

type deferredProvider struct {
	registered bool
}

func (p *deferredProvider) Register(app *ioc.Application) {
	p.registered = true
	app.Container.Singleton("lazy-svc", func(_ *ioc.Container) (any, error) {
		return "lazy-value", nil
	})
}

func (p *deferredProvider) Provides() []string {
	return []string{"lazy-svc"}
}

func TestApp_DeferredProvider(t *testing.T) {
	app := ioc.New()
	dp := &deferredProvider{}
	app.Register(dp)
	app.SetupAll(context.Background())

	if dp.registered {
		t.Error("deferred provider should not be registered eagerly")
	}

	v, err := app.Make("lazy-svc")
	if err != nil {
		t.Fatalf("deferred resolution failed: %v", err)
	}
	if v != "lazy-value" {
		t.Errorf("unexpected value: %v", v)
	}
	if !dp.registered {
		t.Error("deferred provider should now be registered")
	}
}

func TestApp_DeferredProvider_MultipleProvides(t *testing.T) {
	app := ioc.New()
	dp := &multiDeferredProvider{}
	app.Register(dp)
	app.SetupAll(context.Background())

	// 通过任一抽象名触发
	v1, err := app.Make("multi-a")
	if err != nil {
		t.Fatalf("deferred multi-a failed: %v", err)
	}
	if v1 != "value-a" {
		t.Errorf("unexpected: %v", v1)
	}

	// 另一个也应该可用（同一 provider 注册的）
	v2, err := app.Make("multi-b")
	if err != nil {
		t.Fatalf("deferred multi-b failed: %v", err)
	}
	if v2 != "value-b" {
		t.Errorf("unexpected: %v", v2)
	}
}

type multiDeferredProvider struct{}

func (p *multiDeferredProvider) Register(app *ioc.Application) {
	app.Container.Instance("multi-a", "value-a")
	app.Container.Instance("multi-b", "value-b")
}

func (p *multiDeferredProvider) Provides() []string {
	return []string{"multi-a", "multi-b"}
}

// ---------- 延迟提供者重入 ----------

type deferredReentrantProvider struct {
	registered bool
}

func (p *deferredReentrantProvider) Register(app *ioc.Application) {
	p.registered = true
	app.Container.Singleton("deferred-svc", func(_ *ioc.Container) (any, error) {
		return "deferred-value", nil
	})
}

func (p *deferredReentrantProvider) Provides() []string {
	return []string{"deferred-svc"}
}

func TestApp_DeferredProvider_Reentrant(t *testing.T) {
	app := ioc.New()
	app.Container.Instance("base-svc", "base-value")

	dp := &deferredReentrantProvider{}
	app.Register(dp)
	app.SetupAll(context.Background())

	v, err := app.Make("deferred-svc")
	if err != nil {
		t.Fatalf("deferred reentrant Make failed: %v", err)
	}
	if v != "deferred-value" {
		t.Errorf("unexpected value: %v", v)
	}
}

// ---------- 延迟提供者并发 ----------

func TestApp_DeferredProvider_Concurrent(t *testing.T) {
	app := ioc.New()

	var registerCount atomic.Int32
	dp := &concurrentDeferredProvider{count: &registerCount}
	app.Register(dp)
	app.SetupAll(context.Background())

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			v, err := app.Make("concurrent-svc")
			if err != nil {
				t.Errorf("concurrent deferred Make failed: %v", err)
			}
			if v != "concurrent-value" {
				t.Errorf("unexpected value: %v", v)
			}
		}()
	}
	wg.Wait()

	if registerCount.Load() != 1 {
		t.Errorf("deferred provider Register should run once, ran %d times", registerCount.Load())
	}
}

type concurrentDeferredProvider struct {
	count *atomic.Int32
}

func (p *concurrentDeferredProvider) Register(app *ioc.Application) {
	p.count.Add(1)
	app.Container.Singleton("concurrent-svc", func(_ *ioc.Container) (any, error) {
		return "concurrent-value", nil
	})
}

func (p *concurrentDeferredProvider) Provides() []string {
	return []string{"concurrent-svc"}
}

// ---------- 延迟提供者 Setup 错误 ----------

func TestApp_DeferredProvider_SetupError(t *testing.T) {
	app := ioc.New()
	dp := &deferredSetupErrorProvider{}
	app.Register(dp)
	app.SetupAll(context.Background())

	_, err := app.Make("setup-fail-svc")
	if err == nil {
		t.Error("deferred provider with Setup error should propagate error")
	}
}

type deferredSetupErrorProvider struct{}

func (p *deferredSetupErrorProvider) Register(app *ioc.Application) {
	app.Container.Instance("setup-fail-svc", "value")
}
func (p *deferredSetupErrorProvider) Provides() []string { return []string{"setup-fail-svc"} }
func (p *deferredSetupErrorProvider) Setup(_ context.Context, _ *ioc.Application) error {
	return errors.New("deferred setup failed")
}

// ---------- MustMake（Application） ----------

func TestApp_MustMake_Success(t *testing.T) {
	app := ioc.New()
	app.Container.Instance("key", "value")
	v := app.MustMake("key")
	if v != "value" {
		t.Errorf("MustMake returned wrong value: %v", v)
	}
}

func TestApp_MustMake_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustMake should panic on error")
		}
	}()
	app := ioc.New()
	app.MustMake("nonexistent")
}

// ---------- Run ----------

func TestApp_Run_Success(t *testing.T) {
	app := ioc.New()
	p := &mockProvider{}
	app.Register(p)

	err := app.Run(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Run should succeed: %v", err)
	}
	if !p.setupCalled {
		t.Error("Run should call SetupAll")
	}
	if !p.shutdownCalled {
		t.Error("Run should call ShutdownAll")
	}
}

func TestApp_Run_FnError(t *testing.T) {
	app := ioc.New()
	expectedErr := errors.New("run error")

	err := app.Run(context.Background(), func(ctx context.Context) error {
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Errorf("Run should return fn error: got %v", err)
	}
}

func TestApp_Run_SetupError(t *testing.T) {
	app := ioc.New()
	app.Register(&failingSetupProvider{})

	err := app.Run(context.Background(), func(ctx context.Context) error {
		t.Error("fn should not be called when SetupAll fails")
		return nil
	})
	if err == nil {
		t.Error("Run should return SetupAll error")
	}
}

// ---------- Context 取消 ----------

// slowSetupProvider 模拟一个耗时的 Setup，用于验证取消行为。
type slowSetupProvider struct {
	setupCalled bool
}

func (p *slowSetupProvider) Register(_ *ioc.Application) {}
func (p *slowSetupProvider) Setup(ctx context.Context, _ *ioc.Application) error {
	p.setupCalled = true
	return nil
}

func TestApp_SetupAll_Cancelled(t *testing.T) {
	app := ioc.New()
	p1 := &slowSetupProvider{}
	p2 := &slowSetupProvider{}
	app.Register(p1, p2)

	// 使用已取消的 ctx
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := app.SetupAll(ctx)
	if err == nil {
		t.Error("SetupAll with cancelled context should return error")
	}
	// 至少第一个 provider 应被跳过（ctx 在进入循环前已取消）
	if p1.setupCalled || p2.setupCalled {
		t.Error("cancelled context should prevent all providers from being set up")
	}
}

// slowShutdownProvider 记录是否被调用，用于验证 Shutdown 取消时跳过剩余 provider。
type slowShutdownProvider struct {
	name           string
	shutdownCalled bool
}

func (p *slowShutdownProvider) Register(_ *ioc.Application) {}
func (p *slowShutdownProvider) Shutdown(_ context.Context, _ *ioc.Application) error {
	p.shutdownCalled = true
	return nil
}

func TestApp_ShutdownAll_Cancelled(t *testing.T) {
	app := ioc.New()
	p1 := &slowShutdownProvider{name: "first"}
	p2 := &slowShutdownProvider{name: "second"}
	app.Register(p1, p2)
	app.SetupAll(context.Background())

	// 使用已取消的 ctx
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := app.ShutdownAll(ctx)
	if err == nil {
		t.Error("ShutdownAll with cancelled context should return error")
	}
	// 两个 provider 都不应被调用（ctx 在进入循环前已取消）
	if p1.shutdownCalled || p2.shutdownCalled {
		t.Error("cancelled context should skip provider shutdown")
	}
}

// ---------- Run 自动 Serve ----------

// servableMockProvider 同时实现 Provider + Servable，用于测试自动启动
type servableMockProvider struct {
	serveCalled atomic.Bool
	serveErr    error
	serveDone   chan struct{} // Serve 开始后关闭，通知测试
}

func (p *servableMockProvider) Register(_ *ioc.Application) {}
func (p *servableMockProvider) Serve(ctx context.Context) error {
	p.serveCalled.Store(true)
	if p.serveDone != nil {
		close(p.serveDone)
	}
	if p.serveErr != nil {
		return p.serveErr
	}
	<-ctx.Done()
	return nil
}

func TestApp_Run_AutoServe(t *testing.T) {
	app := ioc.New()
	sp := &servableMockProvider{serveDone: make(chan struct{})}
	app.Register(sp)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-sp.serveDone // 等待 Serve 被调用
		cancel()       // 触发关闭
	}()

	err := app.Run(ctx)
	if err != nil {
		t.Fatalf("Run auto-serve should succeed: %v", err)
	}
	if !sp.serveCalled.Load() {
		t.Error("Serve should be called automatically")
	}
}

func TestApp_Run_AutoServe_Error(t *testing.T) {
	app := ioc.New()
	expectedErr := errors.New("serve failed")
	sp := &servableMockProvider{serveErr: expectedErr}
	app.Register(sp)

	err := app.Run(context.Background())
	if !errors.Is(err, expectedErr) {
		t.Errorf("Run should propagate Serve error: got %v", err)
	}
}

func TestApp_Run_WithFn_BackwardCompat(t *testing.T) {
	app := ioc.New()
	sp := &servableMockProvider{}
	fnCalled := false
	app.Register(sp)

	err := app.Run(context.Background(), func(ctx context.Context) error {
		fnCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("Run should succeed: %v", err)
	}
	if !fnCalled {
		t.Error("fn should be called when provided")
	}
	if sp.serveCalled.Load() {
		t.Error("Serve should NOT be called when fn is provided")
	}
}

// servableMockProvider2 第二个 Servable mock，类型不同以避免 Register 去重
type servableMockProvider2 struct {
	serveCalled atomic.Bool
	serveDone   chan struct{}
}

func (p *servableMockProvider2) Register(_ *ioc.Application) {}
func (p *servableMockProvider2) Serve(ctx context.Context) error {
	p.serveCalled.Store(true)
	if p.serveDone != nil {
		close(p.serveDone)
	}
	<-ctx.Done()
	return nil
}

func TestApp_Run_AutoServe_MultipleServables(t *testing.T) {
	app := ioc.New()
	done1 := make(chan struct{})
	done2 := make(chan struct{})
	sp1 := &servableMockProvider{serveDone: done1}
	sp2 := &servableMockProvider2{serveDone: done2}
	app.Register(sp1, sp2)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-done1
		<-done2
		cancel()
	}()

	err := app.Run(ctx)
	if err != nil {
		t.Fatalf("Run should succeed: %v", err)
	}
	if !sp1.serveCalled.Load() || !sp2.serveCalled.Load() {
		t.Error("both Servable providers should be started")
	}
}

func TestApp_Run_WithParentContext(t *testing.T) {
	app := ioc.New()
	app.Register(&mockProvider{})

	ctx, cancel := context.WithCancel(context.Background())

	err := app.Run(ctx, func(ctx context.Context) error {
		// 取消父 ctx，fn 收到的 ctx 应随之取消
		cancel()
		<-ctx.Done()
		return nil
	})
	if err != nil {
		t.Fatalf("Run should succeed: %v", err)
	}
}
