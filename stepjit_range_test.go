package gozero

import (
	"testing"
)

// TestRangeNodeSemantics pins the direct tier's range loops: slice
// element copies, rune decoding, blank forms, and break inside.
func TestRangeNodeSemantics(t *testing.T) {
	rt := exprRuntime(t)
	for _, tc := range []struct {
		src  string
		want any
	}{
		{`s := "héj"; n := 0; for _, r := range s { n++; if r > 200 { n++ } }; return n;`, int64(4)},
		{`s := "abc"; n := 0; for range s { n++ }; return n;`, int64(3)},
		{`s := "abc"; n := 0; for i := range s { if i == 1 { break }; n++ }; return n;`, int64(1)},
	} {
		if err := rt.Supports(tc.src); err != nil {
			t.Errorf("%q did not JIT: %v", tc.src, err)
			continue
		}
		got, err := rt.Eval[any](tc.src, nil)
		if err != nil || got != tc.want {
			t.Errorf("%q: got %v, %v", tc.src, got, err)
		}
	}
}
