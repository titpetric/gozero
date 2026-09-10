package gozero

import (
	"testing"
)

// TestBinOpNodeSemantics drives the operator families through full
// programs on the direct tier, one case per family edge the widths
// share; the oracle test pins the same semantics against compiled
// Go and the pair test against the reflect tier.
func TestBinOpNodeSemantics(t *testing.T) {
	rt := exprRuntime(t)
	for _, tc := range []struct {
		src  string
		want any
	}{
		// The i8/u8 width-fixing bindings bridge (their identity
		// shapes are outside the call table); the operators
		// themselves run direct, so these assert semantics only and
		// the pure-int64 cases assert the tier too.
		{`n := i8(100); m := n * 3; return m;`, int8(44)},           // wraparound
		{`n := u8(6); m := u8(5); q := n ^ m; return q;`, uint8(3)}, // bitwise
		{`n := i8(-1); m := n >> 20; return m;`, int8(-1)},          // signed shift saturates
		{`n := u8(128); m := n >> 1; return m;`, uint8(64)},         // unsigned shift
		{`a := 7; ok := (a & 1) == 1; return ok;`, true},            // comparison
		{`f := 1.0; g := f / 0.0; ok := g > 0.0; return ok;`, true}, // +Inf
		{`s := "Z"; ok := s < "a"; return ok;`, true},               // byte order
	} {
		got, err := rt.Eval[any](tc.src, nil)
		if err != nil || got != tc.want {
			t.Errorf("%q: got %v (%T), %v", tc.src, got, got, err)
		}
	}
}


// TestBinOpNodeTier asserts the pure-int64 operator surface reaches
// the direct tier with no bridges.
func TestBinOpNodeTier(t *testing.T) {
	rt := exprRuntime(t)
	for _, src := range []string{
		`a := 7; ok := (a & 1) == 1; return ok;`,
		`a := 6; b := 3; c := a&^b + a<<2 - b%2; return c;`,
		`f := 1.5; g := f * 2.0; ok := g > 2.9; return ok;`,
		`s := "x"; r := s + "y"; ok := r == "xy"; return ok;`,
	} {
		if err := rt.Supports(src); err != nil {
			t.Errorf("%q did not JIT: %v", src, err)
		}
	}
}
