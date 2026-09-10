package gozero

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func errRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"parse": func(s string) (int64, error) {
			if s == "" {
				return 0, errors.New("empty input")
			}
			return int64(len(s)), nil
		},
		"sprint": func(v any) string { return fmt.Sprint(v) },
		"emsg": func(err error) string {
			if err == nil {
				return "<nil>"
			}
			return err.Error()
		},
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	return rt
}

// TestBindErr covers the hybrid rule: naming the trailing error makes
// it a value the program can test; eliding it keeps the implicit
// check-and-abort. Both spellings are legal Go.
func TestBindErr(t *testing.T) {
	rt := errRuntime(t)

	// Elided: the failing call ends the program.
	if _, err := rt.Eval[any](`n := parse(""); return n;`, nil); err == nil || err.Error() != "empty input" {
		t.Fatalf("implicit check: err = %v", err)
	}

	// Named: the error is a value, tested with if.
	src := `n, err := parse(""); if err != nil { return -1 }; return n;`
	got, err := rt.Eval[int64](src, nil)
	if err != nil || got != -1 {
		t.Fatalf("got %v, %v", got, err)
	}

	src = `n, err := parse("abc"); if err != nil { return -1 }; return n;`
	if got, err := rt.Eval[int64](src, nil); err != nil || got != 3 {
		t.Fatalf("got %v, %v", got, err)
	}

	// The bound error is an ordinary value: it passes to bindings.
	src = `n, err := parse(""); s := emsg(err); _ = n; return s;`
	if got, err := rt.Eval[string](src, nil); err != nil || got != "empty input" {
		t.Fatalf("got %q, %v", got, err)
	}

	// The blank identifier discards, including the error.
	src = `n, _ := parse(""); return n;`
	if got, err := rt.Eval[int64](src, nil); err != nil || got != 0 {
		t.Fatalf("blank error: got %v, %v", got, err)
	}
	src = `_, err := parse("xy"); return err == nil;`
	if got, err := rt.Eval[bool](src, nil); err != nil || !got {
		t.Fatalf("blank value: got %v, %v", got, err)
	}

	// One name too many is still the arity error.
	if _, err := rt.Compile(`a, b, c := parse("x"); return a;`); err == nil || !strings.Contains(err.Error(), "cannot assign") {
		t.Fatalf("arity: err = %v", err)
	}
}
