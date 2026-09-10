package gozero

import (
	"testing"
)

// TestBridgeArgMaterialize drives every argument kind through a bridged
// call: the shape below is outside the call table, so each argument
// materializes through bridgeArg.
func TestBridgeArgMaterialize(t *testing.T) {
	rt := exprRuntime(t)
	type trio struct{ A, B, C int64 }
	if err := rt.Bind("mix", func(a int64, s string, v any, xs []int64) string {
		return s
	}); err != nil {
		t.Fatal(err)
	}
	src := `xs := seq()
n := 2
out := mix(n*3, "lit"+"eral", xs[0], xs)
return out`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fn.Exec[string](nil)
	if err != nil || got != "literal" {
		t.Fatalf("got %q, %v", got, err)
	}
	_ = trio{}
}
