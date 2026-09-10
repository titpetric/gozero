package gozero

import (
	"strings"
	"testing"
)

// TestFieldSetExpr covers an operator expression as the value of a
// field write, and the frame walk behind a nested call inside one.
func TestFieldSetExpr(t *testing.T) {
	rt := exprRuntime(t)
	type box struct{ N int64 }
	if err := rt.Bind("mkbox", func() *box { return &box{N: 1} }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("bump", func(n int64) int64 { return n + 1 }); err != nil {
		t.Fatal(err)
	}
	got, err := rt.Eval[int64](`b := mkbox(); b.N = b.N*10 + bump(1); return b.N;`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 12 {
		t.Fatalf("got %d", got)
	}

	// The value must still fit the field.
	_, err = rt.Compile(`b := mkbox(); s := "x"; b.N = s + "y"; return b.N;`)
	if err == nil || !strings.Contains(err.Error(), "cannot assign") {
		t.Fatalf("err = %v", err)
	}
}
