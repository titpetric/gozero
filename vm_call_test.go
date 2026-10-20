package gozero

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"testing"
)

// TestProgramChainedAndNested is the same program as one statement:
// json.NewEncoder(dest).Encode(...) chains a method onto a call result,
// and http.NewRequest("GET", "/").Cookies() is that same chain nested
// as an argument.
func TestProgramChainedAndNested(t *testing.T) {
	rt := vmRuntime(t)
	const src = `json.NewEncoder(dest).Encode(http.NewRequest("GET", "/").Cookies());`

	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	var dest bytes.Buffer
	if err := fn.Scan(&dest, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := dest.String(), "[]\n"; got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}
}

// TestProgramOmittedArgument checks that the third parameter of
// http.NewRequest takes its zero value: the request carries a nil
// body.
func TestProgramOmittedArgument(t *testing.T) {
	rt := vmRuntime(t)
	fn, err := rt.Compile(`return http.NewRequest("GET", "https://example.com/x");`)
	if err != nil {
		t.Fatal(err)
	}
	req, err := fn.Exec[*http.Request](nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Body != nil {
		t.Errorf("Body = %v, want nil from the omitted argument", req.Body)
	}
	if req.URL.Path != "/x" {
		t.Errorf("path = %q, want /x", req.URL.Path)
	}
}

// TestProgramUnknownMethod checks that a method missing from the result
// type is a compile error, and the program never starts.
func TestProgramUnknownMethod(t *testing.T) {
	rt := vmRuntime(t)
	_, err := rt.Compile(`
		req := http.NewRequest("GET", "/");
		req.Nope();
	`)
	if err == nil {
		t.Fatal("expected a compile error for the unknown method")
	}
	t.Log(err)
}

// TestInterfaceMethodCall checks a method resolved on an
// interface-typed name. MethodByName on an interface type returns a
// zero Func, which would panic in compileCall; the compiler
// synthesizes the dispatch with ifaceMethodFunc.
func TestInterfaceMethodCall(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("ctxOf", func(ctx context.Context) context.Context { return ctx }); err != nil {
		t.Fatal(err)
	}

	// Done on a cancellable context is a real channel, reached through
	// two interface method calls on a name the program bound.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fn, err := rt.Compile(`ctx := ctxOf(); d := ctx.Done(); return d`)
	if err != nil {
		t.Fatal(err)
	}
	d, err := fn.ExecContext[<-chan struct{}](ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil {
		t.Fatal("Done() = nil, want the cancellable context's channel")
	}

	// Err returns only an error, so the error contract applies: on a
	// cancelled context the statement ends the program with the
	// cancellation cause and binds no value.
	cancel()
	if _, err := rt.EvalContext[any](ctx, `ctxOf().Err();`, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestInterfaceMethodOnNil pins Go parity for a method call on a nil
// interface: a panic inside reflect, arriving as *PanicError through
// the guard, leaving the caller's stack intact.
func TestInterfaceMethodOnNil(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("nilWriter", func() io.Writer { return nil }); err != nil {
		t.Fatal(err)
	}
	_, err := rt.Eval[any](`w := nilWriter(); w.Write();`, nil)
	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *PanicError", err)
	}
}

type msInner struct{ N int }

func (i *msInner) Bump() int { i.N++; return i.N }
func (i msInner) Get() int   { return i.N }

type msOuter struct {
	msInner
	Tag string
}

// TestMethodSets pins Go's method-set rules: a pointer receiver method
// is callable on a value-typed name because a name is a variable and
// has an address, promotion reaches embedded methods through both
// receiver kinds, and mutation through the receiver reaches the slot.
func TestMethodSets(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("url.Parse", url.Parse); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("newOuter", func() msOuter { return msOuter{msInner: msInner{N: 1}} }); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, src string
		want      any
	}{
		{"ptr receiver on var value", `var u url.URL
u.Path = "/x"
s := u.String()
return s`, "/x"},
		{"ptr receiver on bound result", `o := newOuter(); n := o.Bump(); return n`, int(2)},
		{"mutation reaches the slot", `o := newOuter(); o.Bump(); n := o.Bump(); return n`, int(3)},
		{"promoted value receiver", `o := newOuter(); n := o.Get(); return n`, int(1)},
		{"promoted ptr receiver", `o := newOuter(); o.Bump(); n := o.Get(); return n`, int(2)},
	} {
		got, err := rt.Eval[any](tc.src, nil)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %v (%T), want %v", tc.name, got, got, tc.want)
		}
	}

	// The result of a call is not addressable, as in Go.
	if _, err := rt.Compile(`n := newOuter().Bump();`); err == nil {
		t.Error("expected a compile error for a pointer method on a call result")
	} else {
		t.Log(err)
	}

	// A pointer method on a var-declared value runs on the direct
	// tier: the receiver is the frame slot's address.
	if err := rt.Supports(`var u url.URL
u.Path = "/jit"
s := u.String()
return s`); err != nil {
		t.Errorf("pointer method kept the program off the direct tier: %v", err)
	}
}

type ctxKey struct{}

// TestContextAutoFill checks the plumbing rules: a context.Context
// parameter the program does not write receives the execution context,
// a context the program made and passed explicitly wins, and the plain
// Exec falls back to context.Background.
func TestContextAutoFill(t *testing.T) {
	rt := featRuntime(t)
	var got context.Context
	if err := rt.Bind("take", func(ctx context.Context, tag string) (*url.URL, error) {
		got = ctx
		return &url.URL{Path: tag}, nil
	}); err != nil {
		t.Fatal(err)
	}

	// Auto-filled: the source never mentions a context.
	fn, err := rt.Compile(`return take("auto");`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), ctxKey{}, "outer")
	u, err := fn.ExecContext[*url.URL](ctx, nil)
	if err != nil || u.Path != "auto" {
		t.Fatalf("got %v, %v", u, err)
	}
	if got.Value(ctxKey{}) != "outer" {
		t.Errorf("binding saw %v, want the execution context", got)
	}

	// Explicit: a program-bound context consumes the parameter.
	fn, err = rt.Compile(`own := context.Background(); return take(own, "explicit");`)
	if err != nil {
		t.Fatal(err)
	}
	u, err = fn.ExecContext[*url.URL](ctx, nil)
	if err != nil || u.Path != "explicit" {
		t.Fatalf("got %v, %v", u, err)
	}
	if got.Value(ctxKey{}) != nil {
		t.Error("the explicit context leaked the execution context's value")
	}

	// Exec without a context still runs, on Background.
	fn, err = rt.Compile(`return take("plain");`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[*url.URL](nil); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("binding saw a nil context")
	}

	// Auto-fill reaches http.NewRequestWithContext as advertised.
	fn, err = rt.Compile(`req := http.NewRequestWithContext("GET", "/c"); return req;`)
	if err != nil {
		t.Fatal(err)
	}
	req, err := fn.ExecContext[*http.Request](ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Context().Value(ctxKey{}) != "outer" {
		t.Error("the request did not carry the execution context")
	}
}

// TestVariadic covers packing individual arguments and spreading a
// slice, and the errors around a misplaced spread.
func TestVariadic(t *testing.T) {
	rt := featRuntime(t)
	// The test asserts the tier for every legal form with the result named:
	// a silent fall back to the reflect evaluator would leave the pack
	// and spread nodes untested while the test still passed. A call
	// returned directly (return path.Join(...)) does not reach the JIT
	// at all, because a returned value needs a slot; that is a known
	// planner gap, unrelated to variadic calls.
	for _, src := range []string{
		`s := path.Join("a", "b", "c"); return s;`,
		`parts := strings.Fields("x y z"); s := path.Join(parts...); return s;`,
		`s := path.Join(); return s;`,
	} {
		if err := rt.Supports(src); err != nil {
			t.Errorf("%s: did not JIT: %v", src, err)
		}
	}

	fn, err := rt.Compile(`return path.Join("a", "b", "c");`)
	if err != nil {
		t.Fatal(err)
	}
	s, err := fn.Exec[string](nil)
	if err != nil || s != "a/b/c" {
		t.Fatalf("pack: got %q, %v", s, err)
	}

	fn, err = rt.Compile(`parts := strings.Fields("x y z"); return path.Join(parts...);`)
	if err != nil {
		t.Fatal(err)
	}
	s, err = fn.Exec[string](nil)
	if err != nil || s != "x/y/z" {
		t.Fatalf("spread: got %q, %v", s, err)
	}

	// Zero variadic arguments is a legal call.
	fn, err = rt.Compile(`return path.Join();`)
	if err != nil {
		t.Fatal(err)
	}
	if s, err := fn.Exec[string](nil); err != nil || s != "" {
		t.Fatalf("empty: got %q, %v", s, err)
	}

	for _, bad := range []string{
		`u := url.Parse("/a"); return path.Join(u...);`,
		`parts := strings.Fields("x"); return url.Parse(parts...);`,
		`parts := strings.Fields("x"); return path.Join(parts..., "y");`,
	} {
		if _, err := rt.Compile(bad); err == nil {
			t.Errorf("%s: expected a compile error", bad)
		}
	}
}
