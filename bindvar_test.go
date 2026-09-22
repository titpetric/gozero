package gozero

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// bvRuntime binds the recorders every case in this file reads its
// result through, so a program's effect is observable without a
// return value.
func bvRuntime(t *testing.T) (*Runtime, *bvSeen) {
	t.Helper()
	seen := &bvSeen{}
	rt := NewRuntime()
	bind := func(name string, fn any) {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatalf("bind %s: %v", name, err)
		}
	}
	bind("recordS", func(s string) error { seen.s = s; return nil })
	bind("recordN", func(n int) error { seen.n = n; return nil })
	bind("recordD", func(d time.Duration) error { seen.d = d; return nil })
	bind("recordL", func(v []string) error { seen.l = append([]string(nil), v...); return nil })
	bind("recordE", func(e error) error { seen.e = e; return nil })
	bind("first", func(v []string) (string, error) {
		if len(v) == 0 {
			return "", nil
		}
		return v[0], nil
	})
	bind("pokeL", func(v []string) error {
		if len(v) > 0 {
			v[0] = "poked"
		}
		return nil
	})
	bind("setL", func(p *[]string) error { *p = []string{"replaced"}; return nil })
	bind("bump", func(p *int) error { *p++; return nil })
	bind("delete", Delete)
	bind("lenM", func(m map[string]int) error { seen.n = len(m); return nil })
	return rt, seen
}

type bvSeen struct {
	s string
	n int
	d time.Duration
	l []string
	e error
}

func bvRun(t *testing.T, rt *Runtime, src string) error {
	t.Helper()
	fn, err := rt.Compile(src)
	if err != nil {
		return err
	}
	_, err = fn.Exec[any](nil)
	return err
}

// TestBindVarRead pins that a value binding is read in argument
// position and keeps the static type it was bound with.
func TestBindVarRead(t *testing.T) {
	rt, seen := bvRuntime(t)
	args := []string{"prog", "-v"}
	if err := rt.BindVar("os.Args", args); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindVar("time.Hour", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindVar("io.EOF", io.EOF); err != nil {
		t.Fatal(err)
	}

	if err := bvRun(t, rt, `recordL(os.Args)`); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(seen.l, ","); got != "prog,-v" {
		t.Errorf("os.Args read as %q, want prog,-v", got)
	}

	if err := bvRun(t, rt, `recordS(first(os.Args))`); err != nil {
		t.Fatal(err)
	}
	if seen.s != "prog" {
		t.Errorf("first(os.Args) = %q, want prog", seen.s)
	}

	if err := bvRun(t, rt, `recordD(time.Hour)`); err != nil {
		t.Fatal(err)
	}
	if seen.d != time.Hour {
		t.Errorf("time.Hour = %v, want 1h", seen.d)
	}

	if err := bvRun(t, rt, `recordE(io.EOF)`); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(seen.e, io.EOF) {
		t.Errorf("io.EOF = %v, want EOF", seen.e)
	}
}

// TestBindVarStaticType pins that a value binding is type-checked
// against the parameter when the program compiles, not when it runs.
func TestBindVarStaticType(t *testing.T) {
	rt, _ := bvRuntime(t)
	if err := rt.BindVar("time.Hour", time.Hour); err != nil {
		t.Fatal(err)
	}
	err := bvRun(t, rt, `recordN(time.Hour)`)
	if err == nil || !strings.Contains(err.Error(), "cannot use time.Duration as int") {
		t.Fatalf("want a compile-time type error, got %v", err)
	}
}

// TestBindVarImmutable is the rule the whole distinction exists for: a
// binding the host passed by value cannot be assigned, and cannot have
// its address taken either, because both would write a copy.
func TestBindVarImmutable(t *testing.T) {
	rt, _ := bvRuntime(t)
	if err := rt.BindVar("io.EOF", io.EOF); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindVar("os.Args", []string{"a"}); err != nil {
		t.Fatal(err)
	}

	err := bvRun(t, rt, `io.EOF = nil`)
	if err == nil || !strings.Contains(err.Error(), "bound by value") {
		t.Fatalf("assigning an immutable binding: want a bound-by-value error, got %v", err)
	}
	err = bvRun(t, rt, `setL(&os.Args)`)
	if err == nil || !strings.Contains(err.Error(), "cannot take the address") {
		t.Fatalf("addressing an immutable binding: want an addressability error, got %v", err)
	}
	// Reading still works, and the shallow copy is Go's: the elements
	// behind an immutable slice binding stay shared.
	if err := bvRun(t, rt, `pokeL(os.Args)`); err != nil {
		t.Fatal(err)
	}
}

// TestBindVarMutable covers the pointer form end to end: assignment,
// address-of into a *T parameter, and the host seeing both.
func TestBindVarMutable(t *testing.T) {
	rt, seen := bvRuntime(t)
	args := []string{"prog", "-v"}
	counter := 0
	if err := rt.BindVar("os.Args", Mutable(&args)); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindVar("counter", Mutable(&counter)); err != nil {
		t.Fatal(err)
	}

	// The name denotes the pointee, so reading is unchanged.
	if err := bvRun(t, rt, `recordL(os.Args)`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen.l, ",") != "prog,-v" {
		t.Fatalf("read as %v", seen.l)
	}

	// &name produces the pointer the *T parameter wants.
	if err := bvRun(t, rt, `setL(&os.Args)`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(args, ",") != "replaced" {
		t.Errorf("setL(&os.Args) left %v, want [replaced]", args)
	}

	if err := bvRun(t, rt, `bump(&counter)`); err != nil {
		t.Fatal(err)
	}
	if counter != 1 {
		t.Errorf("bump(&counter) left %d, want 1", counter)
	}

	// Assignment writes the host's variable.
	if err := bvRun(t, rt, "xs := first(os.Args)\nrecordS(xs)"); err != nil {
		t.Fatal(err)
	}
	if seen.s != "replaced" {
		t.Errorf("first(os.Args) = %q after the write, want replaced", seen.s)
	}
}

// TestBindVarAssign pins assignment to a mutable binding from a call,
// a literal and a name.
func TestBindVarAssign(t *testing.T) {
	rt, seen := bvRuntime(t)
	n := 0
	s := ""
	if err := rt.BindVar("cfg.Count", Mutable(&n)); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindVar("cfg.Name", Mutable(&s)); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("two", func() (int, error) { return 2, nil }); err != nil {
		t.Fatal(err)
	}

	if err := bvRun(t, rt, `cfg.Count = 7`); err != nil {
		t.Fatal(err)
	}
	if n != 7 {
		t.Errorf("cfg.Count = %d, want 7", n)
	}
	if err := bvRun(t, rt, `cfg.Count = two()`); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("cfg.Count = %d after the call form, want 2", n)
	}
	if err := bvRun(t, rt, "v := \"named\"\ncfg.Name = v"); err != nil {
		t.Fatal(err)
	}
	if s != "named" {
		t.Errorf("cfg.Name = %q, want named", s)
	}
	if err := bvRun(t, rt, `recordN(cfg.Count)`); err != nil {
		t.Fatal(err)
	}
	if seen.n != 2 {
		t.Errorf("read back %d, want 2", seen.n)
	}
}

// TestDerefAndAddr pins the two reference operators against Go's
// rules: * reads through a pointer, & takes an address, and neither
// silently substitutes for the other.
func TestDerefAndAddr(t *testing.T) {
	rt, seen := bvRuntime(t)
	n := 5
	if err := rt.BindVar("counter", Mutable(&n)); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("ptr", func() (*int, error) { return &n, nil }); err != nil {
		t.Fatal(err)
	}

	// *p on a program name holding a pointer.
	if err := bvRun(t, rt, "p := ptr()\nrecordN(*p)"); err != nil {
		t.Fatal(err)
	}
	if seen.n != 5 {
		t.Errorf("*p = %d, want 5", seen.n)
	}

	// Writing through it.
	if err := bvRun(t, rt, "p := ptr()\n*p = 9"); err != nil {
		t.Fatal(err)
	}
	if n != 9 {
		t.Errorf("*p = 9 left %d", n)
	}

	// A value cannot fill a *T parameter, as in Go.
	err := bvRun(t, rt, `bump(counter)`)
	if err == nil || !strings.Contains(err.Error(), "cannot use int as *int") {
		t.Fatalf("want a pointer-type rejection, got %v", err)
	}
	// And a pointer cannot fill a T parameter.
	err = bvRun(t, rt, "p := ptr()\nrecordN(p)")
	if err == nil || !strings.Contains(err.Error(), "cannot use *int as int") {
		t.Fatalf("want a value-type rejection, got %v", err)
	}
	// Dereferencing a non-pointer is a compile error, not a panic.
	err = bvRun(t, rt, `recordN(*counter)`)
	if err == nil || !strings.Contains(err.Error(), "not a pointer") {
		t.Fatalf("want a deref rejection, got %v", err)
	}
}

// TestBindVarRejections pins what BindVar refuses to register.
func TestBindVarRejections(t *testing.T) {
	rt := NewRuntime()
	if err := rt.BindVar("f", strings.ToUpper); err == nil || !strings.Contains(err.Error(), "register it with Bind") {
		t.Errorf("a func should be rejected, got %v", err)
	}
	if err := rt.BindVar("z", nil); err == nil || !strings.Contains(err.Error(), "nil value") {
		t.Errorf("nil should be rejected, got %v", err)
	}
	var p *int
	if err := rt.BindVar("p", p); err == nil || !strings.Contains(err.Error(), "nil *int") {
		t.Errorf("a nil pointer should be rejected, got %v", err)
	}
	// A program name cannot shadow a value binding, the rule Bind has.
	if err := rt.BindVar("cursor", 1); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("id", func(n int) (int, error) { return n, nil }); err != nil {
		t.Fatal(err)
	}
	_, err := rt.Compile("cursor := id(1)")
	if err == nil || !strings.Contains(err.Error(), "shadows a binding") {
		t.Errorf("shadowing should be rejected, got %v", err)
	}
}

// TestPtrCopy pins the copy semantics of Ptr, which is the part a
// caller gets wrong: Ptr(x) points at a copy of x, never at x.
func TestPtrCopy(t *testing.T) {
	host := []string{"original"}
	p := Ptr(host)
	*p = []string{"through the copy"}
	if host[0] != "original" {
		t.Errorf("Ptr aliased its argument, host is now %v", host)
	}
	if Ptr(3) == nil || *Ptr(3) != 3 {
		t.Error("Ptr did not return a pointer to its argument")
	}

	// The elements behind a slice are shared either way, which is what
	// makes the header/element distinction worth stating.
	shared := []string{"a"}
	q := Ptr(shared)
	(*q)[0] = "b"
	if shared[0] != "b" {
		t.Errorf("elements should be shared, host is %v", shared)
	}
}

// TestMutableNeedsAnAddress is the flaw the Ref marker exists to
// prevent: a bound pointer value and a writable handle are the same
// shape, so intent cannot be read off the type. io.EOF is the case
// that bites, since an any erases the interface and leaves a
// *errors.errorString behind.
func TestMutableNeedsAnAddress(t *testing.T) {
	rt, _ := bvRuntime(t)
	if err := rt.BindVar("io.EOF", io.EOF); err != nil {
		t.Fatal(err)
	}
	// Bound by value despite the dynamic type being a pointer.
	err := bvRun(t, rt, `io.EOF = nil`)
	if err == nil || !strings.Contains(err.Error(), "bound by value") {
		t.Fatalf("io.EOF should be immutable, got %v", err)
	}
	// And it still reads as an error, not as the pointee.
	if err := bvRun(t, rt, `recordE(io.EOF)`); err != nil {
		t.Fatal(err)
	}
}

// TestDelete covers the delete that works on a map behind an any, on
// both the type-switched and the reflect paths.
func TestDelete(t *testing.T) {
	m := map[string]int{"a": 1, "b": 2}
	if err := Delete(m, "a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["a"]; ok {
		t.Error("a survived the delete")
	}
	// A missing key is not an error, as in Go.
	if err := Delete(m, "zz"); err != nil {
		t.Errorf("deleting a missing key: %v", err)
	}
	// The reflect path: a map type the switch does not name.
	type key struct{ N int }
	rm := map[key]string{{N: 1}: "one", {N: 2}: "two"}
	if err := Delete(rm, key{N: 1}); err != nil {
		t.Fatal(err)
	}
	if len(rm) != 1 {
		t.Errorf("reflect path left %d entries, want 1", len(rm))
	}
	// A convertible key.
	im := map[int64]string{7: "seven"}
	if err := Delete(im, 7); err != nil {
		t.Fatal(err)
	}
	if len(im) != 0 {
		t.Errorf("a convertible key did not delete: %v", im)
	}
	// A nil map deletes nothing and does not error, as in Go.
	var nilm map[string]int
	if err := Delete(nilm, "a"); err != nil {
		t.Errorf("nil map: %v", err)
	}
	// Not a map.
	if err := Delete(42, "a"); err == nil || !strings.Contains(err.Error(), "not a map") {
		t.Errorf("want a not-a-map error, got %v", err)
	}
	if err := Delete(m, 1); err == nil || !strings.Contains(err.Error(), "as a map[string]int key") {
		t.Errorf("want a key-type error, got %v", err)
	}
}

// TestDeleteBinding drives Delete from a program, which is the form
// the host registers.
func TestDeleteBinding(t *testing.T) {
	rt, seen := bvRuntime(t)
	m := map[string]int{"a": 1, "b": 2}
	if err := rt.BindVar("cache", m); err != nil {
		t.Fatal(err)
	}
	if err := bvRun(t, rt, "delete(cache, \"a\")\nlenM(cache)"); err != nil {
		t.Fatal(err)
	}
	if seen.n != 1 {
		t.Errorf("map has %d entries, want 1", seen.n)
	}
	if _, ok := m["a"]; ok {
		t.Error("the host's map still holds a")
	}
}

// TestBindVarTier records which of these forms reach the direct tier.
// A read does; a write does not yet, and must fall back whole rather
// than drop the statement, which is what the planner rejection buys.
func TestBindVarTier(t *testing.T) {
	rt, _ := bvRuntime(t)
	n := 0
	if err := rt.BindVar("counter", Mutable(&n)); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindVar("label", "hello"); err != nil {
		t.Fatal(err)
	}
	// SS_E and PS_i64 are in the shape table; the recorders above are
	// not, so the tier assertions use bindings that are.
	if err := rt.Bind("pair", func(a, b string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("count", func(p *int, s string) int64 { return int64(*p + len(s)) }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Supports(`pair(label, "x")`); err != nil {
		t.Errorf("reading an immutable binding should be direct: %v", err)
	}
	if err := rt.Supports(`n := count(&counter, "x")`); err != nil {
		t.Errorf("addressing a mutable binding should be direct: %v", err)
	}
	if err := rt.Supports("counter = 3"); err == nil {
		t.Error("a write should report its fallback")
	}
	// The fallback still executes the write.
	if err := bvRun(t, rt, "counter = 3"); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("the reflect evaluator left counter at %d, want 3", n)
	}
}

// TestBindVarMutableSeesHostWrites pins that a mutable binding reads
// live storage: a host write between runs is visible to a program
// compiled before it.
func TestBindVarMutableSeesHostWrites(t *testing.T) {
	rt, seen := bvRuntime(t)
	s := "before"
	if err := rt.BindVar("label", Mutable(&s)); err != nil {
		t.Fatal(err)
	}
	fn, err := rt.Compile("recordS(label)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	if seen.s != "before" {
		t.Fatalf("first read %q", seen.s)
	}
	s = "after"
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	if seen.s != "after" {
		t.Errorf("second read %q, want after", seen.s)
	}
}
