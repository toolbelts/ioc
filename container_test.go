package ioc_test

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/toolbelts/ioc"
)

// ---------- Bind / Transient ----------

func TestBind_Transient(t *testing.T) {
	c := ioc.NewContainer()
	callCount := 0
	c.Bind("counter", func(_ *ioc.Container) (any, error) {
		callCount++
		return callCount, nil
	})

	v1, _ := c.Make("counter")
	v2, _ := c.Make("counter")
	if v1.(int) == v2.(int) {
		t.Error("transient binding should return new value each time")
	}
}

func TestBind_OverwritesPrevious(t *testing.T) {
	c := ioc.NewContainer()
	c.Bind("svc", func(_ *ioc.Container) (any, error) { return "old", nil })
	c.Bind("svc", func(_ *ioc.Container) (any, error) { return "new", nil })

	v, _ := c.Make("svc")
	if v != "new" {
		t.Errorf("rebind should overwrite: got %v", v)
	}
}

// ---------- Singleton ----------

func TestSingleton(t *testing.T) {
	c := ioc.NewContainer()
	callCount := 0
	c.Singleton("counter", func(_ *ioc.Container) (any, error) {
		callCount++
		return callCount, nil
	})

	v1, _ := c.Make("counter")
	v2, _ := c.Make("counter")
	if v1 != v2 {
		t.Errorf("singleton should return same value: got %v and %v", v1, v2)
	}
	if callCount != 1 {
		t.Errorf("factory should run once: ran %d times", callCount)
	}
}

func TestSingleton_FactoryError(t *testing.T) {
	c := ioc.NewContainer()
	c.Singleton("fail", func(_ *ioc.Container) (any, error) {
		return nil, errors.New("boom")
	})

	_, err := c.Make("fail")
	if err == nil {
		t.Fatal("expected error from failing factory")
	}
	if !strings.Contains(err.Error(), "fail") {
		t.Errorf("error should mention abstract name: %v", err)
	}
}

func TestSingleton_ConcurrentResolve(t *testing.T) {
	c := ioc.NewContainer()

	var callCount atomic.Int32
	c.Singleton("svc", func(_ *ioc.Container) (any, error) {
		callCount.Add(1)
		return "singleton-value", nil
	})

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	results := make([]any, goroutines)
	errs := make([]error, goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = c.Make("svc")
		}(i)
	}
	wg.Wait()

	if callCount.Load() != 1 {
		t.Errorf("factory should run exactly once, ran %d times", callCount.Load())
	}

	for i := range goroutines {
		if errs[i] != nil {
			t.Errorf("goroutine %d got error: %v", i, errs[i])
		}
		if results[i] != "singleton-value" {
			t.Errorf("goroutine %d got unexpected value: %v", i, results[i])
		}
	}
}

// ---------- Instance ----------

func TestInstance(t *testing.T) {
	c := ioc.NewContainer()
	c.Instance("config", "production")
	v, err := c.Make("config")
	if err != nil || v != "production" {
		t.Errorf("Instance should store and return value: %v, %v", v, err)
	}
}

func TestInstance_OverwritesSingleton(t *testing.T) {
	c := ioc.NewContainer()
	c.Singleton("svc", func(_ *ioc.Container) (any, error) { return "from-factory", nil })
	c.Make("svc") // 触发 factory
	c.Instance("svc", "direct")

	v, _ := c.Make("svc")
	if v != "direct" {
		t.Errorf("Instance should overwrite cached singleton: got %v", v)
	}
}

// ---------- Alias ----------

func TestAlias(t *testing.T) {
	c := ioc.NewContainer()
	c.Instance("real-name", 42)
	c.Alias("nickname", "real-name")
	v, err := c.Make("nickname")
	if err != nil || v != 42 {
		t.Errorf("alias resolution failed: %v, %v", v, err)
	}
}

func TestAlias_Chain(t *testing.T) {
	c := ioc.NewContainer()
	c.Instance("original", "hello")
	c.Alias("level1", "original")
	c.Alias("level2", "level1")
	v, err := c.Make("level2")
	if err != nil || v != "hello" {
		t.Errorf("chained alias failed: %v, %v", v, err)
	}
}

func TestAlias_SelfPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("self-referencing alias should panic")
		}
	}()
	c := ioc.NewContainer()
	c.Alias("x", "x")
}

func TestAlias_CircularPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("circular alias chain should panic")
		}
	}()
	c := ioc.NewContainer()
	c.Alias("a", "b")
	c.Alias("b", "a")
	c.Make("a")
}

// ---------- Make 错误 ----------

func TestMake_NotBound(t *testing.T) {
	c := ioc.NewContainer()
	_, err := c.Make("nonexistent")
	if err == nil {
		t.Error("expected error for unbound abstract")
	}
}

func TestMake_FactoryError(t *testing.T) {
	c := ioc.NewContainer()
	c.Bind("fail", func(_ *ioc.Container) (any, error) {
		return nil, errors.New("factory error")
	})

	_, err := c.Make("fail")
	if err == nil {
		t.Fatal("expected error")
	}
	// 错误应包含 abstract 名
	if !strings.Contains(err.Error(), "fail") {
		t.Errorf("error should mention abstract name: %v", err)
	}
}

// ---------- MustMake ----------

func TestMustMake_Success(t *testing.T) {
	c := ioc.NewContainer()
	c.Instance("key", "value")
	v := c.MustMake("key")
	if v != "value" {
		t.Errorf("MustMake returned wrong value: %v", v)
	}
}

func TestMustMake_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustMake should panic on error")
		}
	}()
	c := ioc.NewContainer()
	c.MustMake("nonexistent")
}

// ---------- 可重入 Make ----------

func TestMake_Reentrant_Singleton(t *testing.T) {
	c := ioc.NewContainer()

	c.Singleton("dep", func(_ *ioc.Container) (any, error) {
		return "dependency", nil
	})

	c.Singleton("svc", func(c *ioc.Container) (any, error) {
		dep, err := c.Make("dep")
		if err != nil {
			return nil, err
		}
		return "service+" + dep.(string), nil
	})

	v, err := c.Make("svc")
	if err != nil {
		t.Fatalf("reentrant Make failed: %v", err)
	}
	if v != "service+dependency" {
		t.Errorf("unexpected value: %v", v)
	}
}

func TestMake_Reentrant_Transient(t *testing.T) {
	c := ioc.NewContainer()

	c.Singleton("dep", func(_ *ioc.Container) (any, error) {
		return "dep-value", nil
	})

	c.Bind("svc", func(c *ioc.Container) (any, error) {
		dep, err := c.Make("dep")
		if err != nil {
			return nil, err
		}
		return "transient+" + dep.(string), nil
	})

	v, err := c.Make("svc")
	if err != nil {
		t.Fatalf("reentrant transient Make failed: %v", err)
	}
	if v != "transient+dep-value" {
		t.Errorf("unexpected value: %v", v)
	}
}

func TestMake_Reentrant_DeepChain(t *testing.T) {
	c := ioc.NewContainer()

	c.Singleton("a", func(_ *ioc.Container) (any, error) { return "A", nil })
	c.Singleton("b", func(c *ioc.Container) (any, error) {
		a, _ := c.Make("a")
		return "B+" + a.(string), nil
	})
	c.Singleton("c", func(c *ioc.Container) (any, error) {
		b, _ := c.Make("b")
		return "C+" + b.(string), nil
	})

	v, err := c.Make("c")
	if err != nil {
		t.Fatalf("deep reentrant chain failed: %v", err)
	}
	if v != "C+B+A" {
		t.Errorf("unexpected value: %v", v)
	}
}

// ---------- Bound ----------

func TestBound(t *testing.T) {
	c := ioc.NewContainer()

	if c.Bound("svc") {
		t.Error("should not be bound before registration")
	}

	c.Bind("svc", func(_ *ioc.Container) (any, error) { return nil, nil })
	if !c.Bound("svc") {
		t.Error("should be bound after Bind")
	}
}

func TestBound_Instance(t *testing.T) {
	c := ioc.NewContainer()
	c.Instance("key", "value")
	if !c.Bound("key") {
		t.Error("should be bound after Instance")
	}
}

func TestBound_Alias(t *testing.T) {
	c := ioc.NewContainer()
	c.Instance("real", "value")
	c.Alias("alias", "real")
	if !c.Bound("alias") {
		t.Error("alias should be recognized as bound")
	}
}

// ---------- Flush ----------

func TestFlush(t *testing.T) {
	c := ioc.NewContainer()
	c.Instance("key", "value")
	c.Bind("svc", func(_ *ioc.Container) (any, error) { return nil, nil })
	c.Alias("a", "key")
	c.Flush()

	if c.Bound("key") || c.Bound("svc") || c.Bound("a") {
		t.Error("Flush should remove all bindings, instances, and aliases")
	}
}

// ---------- OnResolving ----------

func TestOnResolving(t *testing.T) {
	c := ioc.NewContainer()
	var resolved []string
	c.OnResolving(func(abstract string, _ any) {
		resolved = append(resolved, abstract)
	})
	c.Bind("svc", func(_ *ioc.Container) (any, error) { return "ok", nil })
	c.Make("svc")
	if len(resolved) != 1 || resolved[0] != "svc" {
		t.Errorf("resolving callback not fired: %v", resolved)
	}
}

func TestOnResolving_MultipleCallbacks(t *testing.T) {
	c := ioc.NewContainer()
	count := 0
	c.OnResolving(func(_ string, _ any) { count++ })
	c.OnResolving(func(_ string, _ any) { count++ })

	c.Bind("svc", func(_ *ioc.Container) (any, error) { return "ok", nil })
	c.Make("svc")
	if count != 2 {
		t.Errorf("all callbacks should fire: got %d", count)
	}
}

func TestOnResolving_Singleton_FiresOnceOnFirstResolve(t *testing.T) {
	c := ioc.NewContainer()
	count := 0
	c.OnResolving(func(_ string, _ any) { count++ })
	c.Singleton("svc", func(_ *ioc.Container) (any, error) { return "ok", nil })

	c.Make("svc")
	c.Make("svc")
	// 回调仅在首次解析时触发，后续命中缓存快速路径不再触发
	if count != 1 {
		t.Errorf("resolving callback should fire exactly once: got %d", count)
	}
}

// ---------- Benchmark ----------

func BenchmarkMake_SingletonHit(b *testing.B) {
	c := ioc.NewContainer()
	c.Singleton("svc", func(_ *ioc.Container) (any, error) {
		return "value", nil
	})
	c.Make("svc")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Make("svc")
		}
	})
}

func BenchmarkMake_Instance(b *testing.B) {
	c := ioc.NewContainer()
	c.Instance("svc", "value")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Make("svc")
		}
	})
}

func BenchmarkMake_Transient(b *testing.B) {
	c := ioc.NewContainer()
	c.Bind("svc", func(_ *ioc.Container) (any, error) {
		return "value", nil
	})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Make("svc")
		}
	})
}
