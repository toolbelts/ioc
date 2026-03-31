package ioc

import "reflect"

// typeName 返回 Go 类型的规范字符串键，用作基于类型的绑定的抽象名。
func typeName[T any]() string {
	return reflect.TypeFor[T]().String()
}

// ---------- 类型安全绑定 ----------

// Provide 为类型 T 注册一个瞬态工厂（每次调用创建新实例）。
func Provide[T any](c *Container, factory func(c *Container) (T, error)) {
	c.Bind(typeName[T](), wrapFactory(factory))
}

// Singleton 为类型 T 注册一个共享工厂（工厂最多执行一次）。
func Singleton[T any](c *Container, factory func(c *Container) (T, error)) {
	c.Singleton(typeName[T](), wrapFactory(factory))
}

// Set 将已构建好的值存储为类型 T 的单例。
func Set[T any](c *Container, value T) {
	c.Instance(typeName[T](), value)
}

// ---------- 类型安全解析 ----------

// Resolve 从容器中查找类型 T 并以完整的类型安全方式返回。
func Resolve[T any](c *Container) (T, error) {
	return resolveFrom[T](c)
}

// MustResolve 与 Resolve 相同，但遇到错误时 panic。
func MustResolve[T any](c *Container) T {
	return must(Resolve[T](c))
}

// ---------- 键导出 ----------

// TypeKey 返回容器中类型 T 使用的字符串键。
// 当需要将键传递给基于字符串的 API（如 Alias() 或 DeferrableProvider.Provides()）时使用。
func TypeKey[T any]() string {
	return typeName[T]()
}

// ---------- 类型安全别名 ----------

// AliasTo 创建一个别名，使 Resolve[Alias] 通过 Target 的绑定进行解析。
// 常用于将接口别名到其具体类型。
func AliasTo[Alias any, Target any](c *Container) {
	c.Alias(typeName[Alias](), typeName[Target]())
}

// ---------- 便捷方法：从 Application 解析 ----------

// From 是从 Application 解析类型 T 的快捷方法。
// 注意：必须走 Application.Make 而非 Container.Make，以支持延迟提供者。
func From[T any](app *Application) (T, error) {
	return resolveFrom[T](app)
}

// MustFrom 与 From 相同，但遇到错误时 panic。
func MustFrom[T any](app *Application) T {
	return must(From[T](app))
}
