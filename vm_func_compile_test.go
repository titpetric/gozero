package gozero

import (
	"strings"
	"testing"
)

// TestCaptureCompile pins the capture bookkeeping: one cell per
// variable however many reads, transitive capture through nested
// literals, and the func-literal signature check against a wanted
// type.
func TestCaptureCompile(t *testing.T) {
	rt := exprRuntime(t)

	// Two closures over one variable share its cell.
	src := `n := 0
a := func() int64 { n = n + 1; return n }
b := func() int64 { n = n + 10; return n }
a()
b()
return n`
	if got, err := rt.Eval[int64](src, nil); err != nil || got != 11 {
		t.Fatalf("shared cell: got %v, %v", got, err)
	}

	// A literal in argument position adopts the parameter's func
	// type; a mismatched signature is a compile error.
	if err := rt.Bind("apply", func(f func(int64) int64, v int64) int64 { return f(v) }); err != nil {
		t.Fatal(err)
	}
	if got, err := rt.Eval[int64](`v := apply(func(n int64) int64 { return n * 3 }, 7); return v;`, nil); err != nil || got != 21 {
		t.Fatalf("literal argument: got %v, %v", got, err)
	}
	_, err := rt.Compile(`v := apply(func(s string) string { return s }, 7); return v;`)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("signature mismatch: err = %v", err)
	}
}
