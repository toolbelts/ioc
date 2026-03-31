package ioc

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// binding 表示一个绑定条目。
type binding struct {
	factory Factory
	shared  bool // true = 单例
}

// singletonEntry 用于无锁的单例解析。
// sync.Once 保证 factory 恰好执行一次，即使多个 goroutine 并发触发。
// done 标记初始化完成，快速路径通过 atomic 读取确保可见性。
type singletonEntry struct {
	once  sync.Once
	done  atomic.Bool
	value any
	err   error
}

// Container 是核心 IoC 容器，管理绑定、单例和别名。
// 单例实例存储在 sync.Map 中，已解析的单例读取无需加锁。
// factory 调用在锁外执行，支持可重入 Make。
type Container struct {
	mu        sync.RWMutex
	bindings  map[string]binding
	instances sync.Map            // string → *singletonEntry（无锁读）
	aliases   map[string]string   // 别名 → 抽象名
	resolving []func(string, any) // 全局解析回调
}

// NewContainer 创建一个空的容器。
func NewContainer() *Container {
	return &Container{
		bindings: make(map[string]binding),
		aliases:  make(map[string]string),
	}
}

// ---------- 绑定 API ----------

// Bind 注册一个瞬态绑定（每次解析都创建新实例）。
func (c *Container) Bind(abstract string, factory Factory) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.instances.Delete(abstract)
	c.bindings[abstract] = binding{factory: factory, shared: false}
}

// Singleton 注册一个共享绑定（工厂最多执行一次）。
func (c *Container) Singleton(abstract string, factory Factory) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.instances.Delete(abstract)
	c.bindings[abstract] = binding{factory: factory, shared: true}
}

// Instance 将已构建好的值存储为单例。
func (c *Container) Instance(abstract string, value any) {
	e := &singletonEntry{value: value}
	e.done.Store(true)
	c.instances.Store(abstract, e)
}

// Alias 创建别名 → 抽象名映射，使 Make(alias) 通过抽象名的绑定解析。
// 别名链会自动跟踪。
func (c *Container) Alias(alias, abstract string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if alias == abstract {
		panic(fmt.Sprintf("ioc: alias %q cannot target itself", alias))
	}
	c.aliases[alias] = abstract
}

// ---------- 解析 ----------

// Make 解析抽象名（或别名）并返回实例。
// 对于共享绑定，后续调用返回缓存的实例。
// 快速路径：无锁读 sync.Map + atomic.Bool；慢路径：短暂持锁获取 binding 后在锁外执行 factory。
func (c *Container) Make(abstract string) (any, error) {
	// 解析别名（需读锁）
	c.mu.RLock()
	name := c.getAlias(abstract)
	c.mu.RUnlock()

	// 快速路径：无锁读已完成初始化的单例
	if entry, ok := c.instances.Load(name); ok {
		e := entry.(*singletonEntry)
		if e.done.Load() {
			return e.value, e.err
		}
		// entry 存在但未完成初始化 → 走慢路径等待 once.Do
	}

	// 慢路径：查找 binding
	c.mu.RLock()
	b, ok := c.bindings[name]
	c.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("ioc: no binding for %q", name)
	}

	if b.shared {
		return c.resolveSingleton(name, b)
	}
	return c.resolveTransient(name, b)
}

// MustMake 与 Make 相同，但遇到错误时 panic。
func (c *Container) MustMake(abstract string) any {
	return must(c.Make(abstract))
}

// resolveSingleton 使用 sync.Once 保证 factory 恰好执行一次。
// factory 在锁外执行，支持可重入 Make。
// 若 factory 返回错误，entry 会从缓存中移除，但 sync.Once 不会重置，
// 因此同一 entry 的后续调用仍返回该错误。需重新 Singleton() 绑定后才能重试。
func (c *Container) resolveSingleton(name string, b binding) (any, error) {
	// get-or-create entry（短暂竞争由 LoadOrStore 处理）
	actual, _ := c.instances.LoadOrStore(name, &singletonEntry{})
	entry := actual.(*singletonEntry)

	var fired bool
	entry.once.Do(func() {
		entry.value, entry.err = b.factory(c)
		if entry.err != nil {
			entry.err = fmt.Errorf("ioc: building %q: %w", name, entry.err)
			// factory 失败时从缓存移除，允许重新绑定后重试
			c.instances.Delete(name)
		} else {
			// 标记完成，使快速路径可安全读取 value/err
			entry.done.Store(true)
		}
		fired = true
	})

	// 仅在首次解析时触发回调，并发等待的 goroutine 不重复触发
	if fired && entry.err == nil {
		c.fireResolving(name, entry.value)
	}

	return entry.value, entry.err
}

// resolveTransient 每次创建新实例（锁外执行 factory）。
func (c *Container) resolveTransient(name string, b binding) (any, error) {
	obj, err := b.factory(c)
	if err != nil {
		return nil, fmt.Errorf("ioc: building %q: %w", name, err)
	}

	c.fireResolving(name, obj)
	return obj, nil
}

// fireResolving 触发解析回调。
func (c *Container) fireResolving(name string, value any) {
	c.mu.RLock()
	cbs := c.resolving
	c.mu.RUnlock()

	if len(cbs) > 0 {
		for _, cb := range cbs {
			cb(name, value)
		}
	}
}

// getAlias 沿别名链查找规范的抽象名（调用方须持有 mu.RLock）。
func (c *Container) getAlias(name string) string {
	const maxDepth = 16
	for range maxDepth {
		target, ok := c.aliases[name]
		if !ok {
			return name
		}
		name = target
	}
	panic(fmt.Sprintf("ioc: alias chain too deep or circular at %q", name))
}

// ---------- 内省 ----------

// Bound 返回抽象名（或别名）是否已绑定或已有实例。
func (c *Container) Bound(abstract string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	abstract = c.getAlias(abstract)
	_, hasInstance := c.instances.Load(abstract)
	_, hasBinding := c.bindings[abstract]
	return hasBinding || hasInstance
}

// Flush 移除所有绑定、实例和别名，完全重置容器。
func (c *Container) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bindings = make(map[string]binding)
	c.instances = sync.Map{}
	c.aliases = make(map[string]string)
	c.resolving = nil
}

// OnResolving 注册一个回调，在抽象名首次解析时触发。
// 对于单例绑定，回调仅在首次解析时触发；对于瞬态绑定，每次解析都会触发。
func (c *Container) OnResolving(cb func(abstract string, value any)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolving = append(c.resolving, cb)
}
