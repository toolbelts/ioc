package ioc

import "fmt"

// Factory 是构造函数类型，接收容器并返回实例。
type Factory func(c *Container) (any, error)

// maker 是 Container 和 Application 共享的解析接口，用于消除泛型层的重复代码。
type maker interface {
	Make(abstract string) (any, error)
}

// resolveFrom 是类型安全解析的核心实现：Make + 类型断言。
func resolveFrom[T any](m maker) (T, error) {
	var zero T
	raw, err := m.Make(typeName[T]())
	if err != nil {
		return zero, err
	}
	typed, ok := raw.(T)
	if !ok {
		return zero, fmt.Errorf(
			"ioc: type assertion failed: want %s, got %T",
			typeName[T](), raw,
		)
	}
	return typed, nil
}

// wrapFactory 将类型安全的工厂函数包装为通用 Factory。
func wrapFactory[T any](factory func(c *Container) (T, error)) Factory {
	return func(c *Container) (any, error) {
		return factory(c)
	}
}

// must 是通用的 panic-on-error 辅助函数，供所有 Must* 方法使用。
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
