package gozero

import (
	"strings"
	"testing"
)

type bvVal struct{ N int }

func (v bvVal) Describe() string { return "val" }

type bvPtr struct{ N int }

func (p *bvPtr) String() string { return "ptr" }
func (p *bvPtr) Bump()          { p.N++ }

type bvCfg struct {
	Name string
	Fn   func() string
	Two  func(a, b string) string
}

// TestMethodSetsOnValueBindings pins Go's method-set rule on a value
// binding: a value-receiver method is callable on T and *T, and a
// pointer-receiver method is callable on an addressable T, which a
// bound name is. The address is the program's per-run cell, so the
// mutation does not reach the host.
func TestMethodSetsOnValueBindings(t *testing.T) {
	rt, seen := bvRuntime(t)
	host := bvPtr{N: 1}
	for name, v := range map[string]any{
		"v":  bvVal{N: 1},
		"pv": host,
		"pp": &bvPtr{N: 1},
	} {
		if err := rt.Bind(name, v); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct{ src, want string }{
		{"s := v.Describe()\nrecordS(s)", "val"},
		{"s := pv.String()\nrecordS(s)", "ptr"},
		{"s := pp.String()\nrecordS(s)", "ptr"},
	} {
		if err := bvRun(t, rt, tc.src); err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if seen.s != tc.want {
			t.Errorf("%s: got %q, want %q", tc.src, seen.s, tc.want)
		}
	}

	// A pointer method that mutates writes the run's cell, not the
	// value the host handed over.
	if err := bvRun(t, rt, "pv.Bump()\npv.Bump()\nrecordN(pv.N)"); err != nil {
		t.Fatal(err)
	}
	if seen.n != 3 {
		t.Errorf("pv.N = %d after two Bumps, want 3", seen.n)
	}
	if host.N != 1 {
		t.Errorf("host reads %d, want its own value untouched", host.N)
	}
	// And the next run starts from the bound value again.
	if err := bvRun(t, rt, "recordN(pv.N)"); err != nil {
		t.Fatal(err)
	}
	if seen.n != 1 {
		t.Errorf("a later run read %d, want the bound value", seen.n)
	}
}

// TestCallThroughAFuncValue covers a callee that is a value rather
// than a binding: a func-typed name, a func-typed field, and a func
// bound under a name.
func TestCallThroughAFuncValue(t *testing.T) {
	rt, seen := bvRuntime(t)
	for name, v := range map[string]any{
		"cfg": bvCfg{
			Name: "c",
			Fn:   func() string { return "from a field" },
			Two:  func(a, b string) string { return a + "+" + b },
		},
		"getFn": func() (func() string, error) { return func() string { return "from a name" }, nil },
		"hook":  func() string { return "from a func binding" },
	} {
		if err := rt.Bind(name, v); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct{ src, want string }{
		{"f := getFn()\ns := f()\nrecordS(s)", "from a name"},
		{"s := cfg.Fn()\nrecordS(s)", "from a field"},
		{"s := hook()\nrecordS(s)", "from a func binding"},
		{"s := cfg.Two(\"a\", \"b\")\nrecordS(s)", "a+b"},
	} {
		if err := bvRun(t, rt, tc.src); err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if seen.s != tc.want {
			t.Errorf("%s: got %q, want %q", tc.src, seen.s, tc.want)
		}
	}

	// The arguments are checked against the func value's signature
	// when the program compiles, the same as a call to a binding.
	err := bvRun(t, rt, "s := cfg.Two(1, \"b\")\nrecordS(s)")
	if err == nil || !strings.Contains(err.Error(), "cannot use") {
		t.Errorf("a wrong argument type should not compile: %v", err)
	}
	// A value that is not a func is not a call.
	err = bvRun(t, rt, "s := cfg.Name()\nrecordS(s)")
	if err == nil || !strings.Contains(err.Error(), "is a string field, not a method") {
		t.Errorf("cfg.Name(): %v", err)
	}
}

// TestCallThroughNilFuncValue pins the run-time half: a nil func is an
// error naming the callee, not a panic from inside reflect.
func TestCallThroughNilFuncValue(t *testing.T) {
	rt, _ := bvRuntime(t)
	if err := rt.Bind("cfg", bvCfg{Name: "c"}); err != nil {
		t.Fatal(err)
	}
	err := bvRun(t, rt, "s := cfg.Fn()\nrecordS(s)")
	if err == nil || !strings.Contains(err.Error(), "the func is nil") {
		t.Errorf("want a nil func error, got %v", err)
	}
}

// TestCallThroughIsBridged records the tier: a callee read per run has
// no funcval to cast at compile time, so the call bridges while its
// neighbours stay direct.
func TestCallThroughIsBridged(t *testing.T) {
	rt, _ := bvRuntime(t)
	if err := rt.Bind("cfg", bvCfg{Fn: func() string { return "x" }}); err != nil {
		t.Fatal(err)
	}
	err := rt.Supports("s := cfg.Fn()\nrecordS(s)")
	if err == nil {
		t.Error("a call through a value should not report the direct tier")
	}
}
