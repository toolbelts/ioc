package ioc_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/toolbelts/ioc"
)

// ---------- 测试类型 ----------

type testLogger struct{ level string }

type iLogger interface{ Log(string) }
type stdLogger struct{}

func (stdLogger) Log(string) {}

// ---------- Provide / Singleton ----------

func TestGeneric_Singleton(t *testing.T) {
	c := ioc.NewContainer()
	ioc.Singleton(c, func(_ *ioc.Container) (*testLogger, error) {
		return &testLogger{level: "debug"}, nil
	})

	l1, err := ioc.Resolve[*testLogger](c)
	if err != nil {
		t.Fatal(err)
	}
	l2, _ := ioc.Resolve[*testLogger](c)
	if l1 != l2 {
		t.Error("generic singleton should return same pointer")
	}
	if l1.level != "debug" {
		t.Errorf("unexpected level: %s", l1.level)
	}
}

func TestGeneric_Provide_Transient(t *testing.T) {
	c := ioc.NewContainer()
	n := 0
	ioc.Provide(c, func(_ *ioc.Container) (*testLogger, error) {
		n++
		return &testLogger{level: fmt.Sprintf("v%d", n)}, nil
	})
	l1, _ := ioc.Resolve[*testLogger](c)
	l2, _ := ioc.Resolve[*testLogger](c)
	if l1.level == l2.level {
		t.Error("transient should create new instance each time")
	}
}

// ---------- Set ----------

func TestGeneric_Set(t *testing.T) {
	c := ioc.NewContainer()
	ioc.Set(c, "hello-world")
	v, err := ioc.Resolve[string](c)
	if err != nil || v != "hello-world" {
		t.Errorf("Set/Resolve failed: %v, %v", v, err)
	}
}

// ---------- Resolve 错误 ----------

func TestResolve_NotBound(t *testing.T) {
	c := ioc.NewContainer()
	_, err := ioc.Resolve[*testLogger](c)
	if err == nil {
		t.Error("expected error for unbound type")
	}
}

func TestResolve_TypeAssertionFail(t *testing.T) {
	c := ioc.NewContainer()
	// 注册 string 类型，但尝试解析为 int
	ioc.Set(c, "hello")
	c.Alias(ioc.TypeKey[int](), ioc.TypeKey[string]())

	_, err := ioc.Resolve[int](c)
	if err == nil {
		t.Fatal("expected type assertion error")
	}
	if !strings.Contains(err.Error(), "type assertion failed") {
		t.Errorf("error should mention type assertion: %v", err)
	}
}

func TestResolve_FactoryError(t *testing.T) {
	c := ioc.NewContainer()
	ioc.Singleton(c, func(_ *ioc.Container) (*testLogger, error) {
		return nil, errors.New("factory boom")
	})

	_, err := ioc.Resolve[*testLogger](c)
	if err == nil {
		t.Fatal("expected error from failing factory")
	}
}

// ---------- MustResolve ----------

func TestMustResolve_Success(t *testing.T) {
	c := ioc.NewContainer()
	ioc.Set(c, 42)
	v := ioc.MustResolve[int](c)
	if v != 42 {
		t.Errorf("MustResolve returned wrong value: %v", v)
	}
}

func TestMustResolve_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustResolve should panic on error")
		}
	}()
	c := ioc.NewContainer()
	ioc.MustResolve[*testLogger](c)
}

// ---------- AliasTo ----------

func TestGeneric_AliasTo(t *testing.T) {
	c := ioc.NewContainer()
	ioc.Singleton(c, func(_ *ioc.Container) (*stdLogger, error) {
		return &stdLogger{}, nil
	})
	ioc.AliasTo[iLogger, *stdLogger](c)

	_, err := ioc.Resolve[iLogger](c)
	if err != nil {
		t.Fatalf("AliasTo resolution failed: %v", err)
	}
}

// ---------- TypeKey ----------

func TestTypeKey(t *testing.T) {
	k := ioc.TypeKey[*testLogger]()
	if k == "" {
		t.Error("TypeKey should not be empty")
	}
	// 相同类型应返回相同 key
	if k != ioc.TypeKey[*testLogger]() {
		t.Error("TypeKey should be deterministic")
	}
}

// ---------- From / MustFrom ----------

func TestFrom(t *testing.T) {
	app := ioc.New()
	ioc.Set(app.Container, "from-app")

	v, err := ioc.From[string](app)
	if err != nil || v != "from-app" {
		t.Errorf("From failed: %v, %v", v, err)
	}
}

func TestMustFrom_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustFrom should panic on error")
		}
	}()
	app := ioc.New()
	ioc.MustFrom[*testLogger](app)
}
