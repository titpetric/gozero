package gozero

import (
	"errors"
	"io"
	"net/url"
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
	for name, v := range map[string]any{
		"recordS": func(s string) error { seen.s = s; return nil },
		"recordN": func(n int) error { seen.n = n; return nil },
		"recordD": func(d time.Duration) error { seen.d = d; return nil },
		"recordL": func(v []string) error { seen.l = append([]string(nil), v...); return nil },
		"recordE": func(e error) error { seen.e = e; return nil },
		"first":   func(v []string) (string, error) { return v[0], nil },
		"pokeL":   func(v []string) error { v[0] = "poked"; return nil },
		"setL":    func(p *[]string) error { *p = []string{"replaced"}; return nil },
		"bump":    func(p *int) error { *p++; return nil },
		"append":  Append,
		"delete":  Delete,
		"lenM":    func(m map[string]int) error { seen.n = len(m); return nil },
	} {
		if err := rt.Bind(name, v); err != nil {
			t.Fatalf("bind %s: %v", name, err)
		}
	}
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

// TestBindValueRead pins that Bind takes a value as readily as a func,
// and the name carries the static type it was bound with.
func TestBindValueRead(t *testing.T) {
	rt, seen := bvRuntime(t)
	for name, v := range map[string]any{
		"os.Args":   []string{"prog", "-v"},
		"time.Hour": time.Hour,
		"io.EOF":    io.EOF,
	} {
		if err := rt.Bind(name, v); err != nil {
			t.Fatal(err)
		}
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
		t.Errorf("first(os.Args) = %q", seen.s)
	}
	if err := bvRun(t, rt, `recordD(time.Hour)`); err != nil {
		t.Fatal(err)
	}
	if seen.d != time.Hour {
		t.Errorf("time.Hour = %v", seen.d)
	}
	if err := bvRun(t, rt, `recordE(io.EOF)`); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(seen.e, io.EOF) {
		t.Errorf("io.EOF = %v", seen.e)
	}

	// The type check is the binding's, and it happens at compile time.
	err := bvRun(t, rt, `recordN(time.Hour)`)
	if err == nil || !strings.Contains(err.Error(), "cannot use time.Duration as int") {
		t.Fatalf("want a compile-time type error, got %v", err)
	}
}

// TestBindValueIsACopy is the rule a host has to know: a value is
// copied in, so a program writing the name writes its own storage.
// The copy is Go's, which is shallow, so the elements stay shared.
func TestBindValueIsACopy(t *testing.T) {
	rt, seen := bvRuntime(t)
	host := []string{"a", "b"}
	if err := rt.Bind("args", host); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("three", func() ([]string, error) { return []string{"1", "2", "3"}, nil }); err != nil {
		t.Fatal(err)
	}

	// Writing the name writes the run's copy, and a read sees it.
	if err := bvRun(t, rt, "args = three()\nrecordL(args)"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen.l, ",") != "1,2,3" {
		t.Errorf("program read %v after the write", seen.l)
	}
	if strings.Join(host, ",") != "a,b" {
		t.Errorf("host reads %v, want its own variable untouched", host)
	}
	// The next run starts from the bound value again.
	if err := bvRun(t, rt, `recordL(args)`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen.l, ",") != "a,b" {
		t.Errorf("a later run read %v, want the bound value", seen.l)
	}
	// The elements are shared either way, the same shallow copy Go
	// makes when a slice is passed to a function.
	if err := bvRun(t, rt, `pokeL(args)`); err != nil {
		t.Fatal(err)
	}
	if host[0] != "poked" {
		t.Errorf("host reads %v, want the element write", host)
	}
}

// TestBindPointerReachesTheHost covers the opt-in: a bound address is
// a *T, and the program writes the host through it. No API says so;
// the & at the Bind call site does.
func TestBindPointerReachesTheHost(t *testing.T) {
	rt, seen := bvRuntime(t)
	host := []string{"a", "b"}
	n := 0
	if err := rt.Bind("args", &host); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("cursor", &n); err != nil {
		t.Fatal(err)
	}

	// * reads through it.
	if err := bvRun(t, rt, `recordL(*args)`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen.l, ",") != "a,b" {
		t.Errorf("*args read as %v", seen.l)
	}
	// *name = v writes the host, and a literal is a legal value.
	if err := bvRun(t, rt, `*args = []string{"1", "2", "3"}`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(host, ",") != "1,2,3" {
		t.Errorf("host reads %v, want the program's write", host)
	}
	// The name is already the pointer a *T parameter wants.
	if err := bvRun(t, rt, `setL(args)`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(host, ",") != "replaced" {
		t.Errorf("host reads %v after setL", host)
	}
	if err := bvRun(t, rt, "*cursor = 41\nbump(cursor)"); err != nil {
		t.Fatal(err)
	}
	if n != 42 {
		t.Errorf("cursor is %d, want 42", n)
	}
	// And Go's rules hold in both directions.
	err := bvRun(t, rt, `recordL(args)`)
	if err == nil || !strings.Contains(err.Error(), "cannot use *[]string as []string") {
		t.Errorf("a pointer should not fill a value parameter: %v", err)
	}
	err = bvRun(t, rt, `recordN(*args)`)
	if err == nil || !strings.Contains(err.Error(), "cannot use []string as int") {
		t.Errorf("want a type error, got %v", err)
	}
}

// TestBindValueCell covers the storage a written name gets: one cell
// per name per program, shared by the write, the address and every
// read, and fresh on every run.
func TestBindValueCell(t *testing.T) {
	rt, seen := bvRuntime(t)
	if err := rt.Bind("args", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	fn, err := rt.Compile("args = []string{\"1\"}\nappend(&args, \"2\")\nrecordL(args)")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := fn.Exec[any](nil); err != nil {
			t.Fatal(err)
		}
		if strings.Join(seen.l, ",") != "1,2" {
			t.Fatalf("run %d read %v, want 1,2 on every run", i, seen.l)
		}
	}
}

// TestBindValueMethods pins that a value binding is a receiver: it
// owns the longest dotted prefix of a path the way a func binding
// does, and what follows is fields and methods.
func TestBindValueMethods(t *testing.T) {
	rt, seen := bvRuntime(t)
	u, err := url.Parse("https://example.com/p?x=1")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("u", u); err != nil {
		t.Fatal(err)
	}
	if err := bvRun(t, rt, `recordS(u.Path)`); err != nil {
		t.Fatal(err)
	}
	if seen.s != "/p" {
		t.Errorf("u.Path = %q", seen.s)
	}
	if err := bvRun(t, rt, "s := u.String()\nrecordS(s)"); err != nil {
		t.Fatal(err)
	}
	if seen.s != "https://example.com/p?x=1" {
		t.Errorf("u.String() = %q", seen.s)
	}
	if err := bvRun(t, rt, "h := u.Hostname()\nrecordS(h)"); err != nil {
		t.Fatal(err)
	}
	if seen.s != "example.com" {
		t.Errorf("u.Hostname() = %q", seen.s)
	}
	err = bvRun(t, rt, `u()`)
	if err == nil || !strings.Contains(err.Error(), "is a value, not a call") {
		t.Errorf("u(): %v", err)
	}
	err = bvRun(t, rt, "s := u.Nope()\nrecordS(s)")
	if err == nil || !strings.Contains(err.Error(), "has no method or field Nope") {
		t.Errorf("u.Nope(): %v", err)
	}
}

// TestBindRejections pins what Bind refuses and what a program may not
// do with a name it registered.
func TestBindRejections(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("z", nil); err == nil || !strings.Contains(err.Error(), "nil value") {
		t.Errorf("nil should be rejected, got %v", err)
	}
	var p *int
	if err := rt.Bind("p", p); err == nil || !strings.Contains(err.Error(), "nil *int") {
		t.Errorf("a nil pointer should be rejected, got %v", err)
	}
	if err := rt.Bind("cursor", 1); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("id", func(n int) (int, error) { return n, nil }); err != nil {
		t.Fatal(err)
	}
	// A program name cannot shadow a value binding, the rule a func
	// binding has.
	_, err := rt.Compile("cursor := id(1)")
	if err == nil || !strings.Contains(err.Error(), "shadows a binding") {
		t.Errorf("shadowing should be rejected, got %v", err)
	}
	// Nor dereference a value that is not a pointer.
	_, err = rt.Compile("n := id(*cursor)")
	if err == nil || !strings.Contains(err.Error(), "not a pointer") {
		t.Errorf("*cursor should be rejected, got %v", err)
	}
}

// TestBindRebindIsNotAnUpdate pins the rule a host gets wrong: a
// second Bind under the same name is a new binding, and neither a
// compiled program nor the next Compile of the same source sees it,
// because the node captured the binding and the compilation is
// cached.
func TestBindRebindIsNotAnUpdate(t *testing.T) {
	rt, seen := bvRuntime(t)
	if err := rt.Bind("label", "first"); err != nil {
		t.Fatal(err)
	}
	const src = `recordS(label)`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	if seen.s != "first" {
		t.Fatalf("baseline read %q", seen.s)
	}
	if err := rt.Bind("label", "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	if seen.s != "first" {
		t.Errorf("the compiled program read %q after a rebind", seen.s)
	}
	again, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	if seen.s != "first" {
		t.Errorf("the cached compilation read %q after a rebind", seen.s)
	}
}

// TestDeleteBinding drives Delete from a program, which is the form
// the host registers.
func TestDeleteBinding(t *testing.T) {
	rt, seen := bvRuntime(t)
	m := map[string]int{"a": 1, "b": 2}
	if err := rt.Bind("cache", m); err != nil {
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
