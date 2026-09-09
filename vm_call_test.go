package gozero

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
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

// TestProgramOmittedArgument checks the third parameter of
// http.NewRequest being filled with its zero value: the request is
// built with a nil body.
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
// type is a compile error, not an execution one.
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
// zero Func, which used to reach compileCall and panic; the compiler
// now synthesizes the dispatch with ifaceMethodFunc.
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
	// cancellation cause instead of binding a value.
	cancel()
	if _, err := rt.EvalContext[any](ctx, `ctxOf().Err();`, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestInterfaceMethodOnNil pins Go parity for a method call on a nil
// interface: a panic inside reflect, arriving as *PanicError through
// the guard rather than unwinding the caller.
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
