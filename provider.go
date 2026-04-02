package ioc

import "context"

// Provider 是所有服务提供者必须实现的最小接口。
// Register 在注册阶段调用，应在这里将服务绑定到容器，不要在这里解析其他服务。
type Provider interface {
	Register(app *Application)
}

// Setupable 是可选接口。如果提供者实现了 Setup，它会在所有提供者注册完成后被调用，
// 因此可以安全地解析跨服务依赖。ctx 由 SetupAll 透传，可用于感知超时或取消。
type Setupable interface {
	Setup(ctx context.Context, app *Application) error
}

// Shutdownable 是可选接口。如果提供者实现了 Shutdown，它会在应用终止时被调用，
// 用于释放连接、刷新缓冲区、关闭文件等。
// 未实现该接口的提供者在关闭时会被静默跳过。
// ctx 由 ShutdownAll 透传，可用于感知超时或取消。
type Shutdownable interface {
	Shutdown(ctx context.Context, app *Application) error
}

// Servable 是可选接口。实现此接口的提供者会被 Run() 自动并发启动。
// 当 Run() 未传入用户函数时，框架自动收集所有 Servable 提供者，并发调用 Serve(ctx)。
// 任一 Serve 返回 error 时 Run 返回该 error，随后执行 ShutdownAll 清理所有资源。
type Servable interface {
	Serve(ctx context.Context) error
}

// DeferrableProvider 是可选接口。实现该接口的提供者不会立即注册，
// 而是记录其 Provides() 列表，在第一次解析其中某个抽象名时按需注册。
type DeferrableProvider interface {
	Provider
	Provides() []string
}
