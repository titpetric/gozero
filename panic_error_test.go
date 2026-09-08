package gozero

import (
	"errors"
	"testing"
)

// TestPanicError checks the boundary: a panic inside a binding comes
// back from Exec as a *PanicError carrying the recovered value and the
// stack, rather than unwinding into the caller.
func TestPanicError(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("boom", func() (string, error) {
		panic("kaboom")
	}); err != nil {
		t.Fatal(err)
	}
	fn, err := rt.Compile(`return boom();`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fn.Exec[string](nil)
	if err == nil {
		t.Fatal("expected the panic to come back as an error")
	}
	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("err is %T, want *PanicError", err)
	}
	if pe.Value != "kaboom" {
		t.Errorf("value = %v, want kaboom", pe.Value)
	}
	if len(pe.Stack) == 0 {
		t.Error("stack is empty")
	}
}

func TestPanicError_Error(t *testing.T) {
	e := &PanicError{Value: "kaboom"}
	want := "exec: binding panicked: kaboom"
	if got := e.Error(); got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}
