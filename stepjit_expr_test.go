package gozero

import (
	"strings"
	"testing"
)

// TestStepJITExprMatchesReflect runs the operator surface down both
// tiers with identical results required; the oracle test pins the
// same sources against compiled Go, so the three agree pairwise.
func TestStepJITExprMatchesReflect(t *testing.T) {
	rt := exprRuntime(t)
	for _, src := range []string{
		`n := i8(127); m := n + 1; return m;`,
		`n := i8(-128); m := i8(-1); q := n / m; return q;`,
		`n := i32(-7); q := n % 3; return q;`,
		`n := u8(200); m := n + 100; return m;`,
		`n := i8(1); m := n << 10; return m;`,
		`n := i8(-8); m := n >> 1; return m;`,
		`a := 6; b := 3; c := a&^b + a&b; return c;`,
		`f := f32(0.1); g := f + f32(0.2); return g;`,
		`f := 0.0; g := f / f; ok := g != g; return ok;`,
		`s := "abc"; ok := s < "abd" && s >= "abc"; return ok;`,
		`s := "x"; r := s + "y" + "z"; return r;`,
		`ok := false; r := !ok; return r;`,
		`n := i8(3); m := ^n; return m;`,
		`n := -5; m := -n; return m;`,
		`s := "abcd"; b := s[2]; return b;`,
		`s := "abcd"; n := len(s); return n;`,
		`p := pnil(); ok := p == nil; return ok;`,
		`q := pval(); ok := q != nil; return ok;`,
	} {
		jit, slow := compilePair(t, rt, src)
		a, errA := jit.Exec[any](nil)
		b, errB := slow.Exec[any](nil)
		if (errA == nil) != (errB == nil) || a != b {
			t.Errorf("%q: jit %v (%v), reflect %v (%v)", src, a, errA, b, errB)
		}
	}

	// Short-circuiting skips the right side on both tiers.
	var rec []string
	src := `c := sideRec(rec, "L", false) && sideRec(rec, "R", true); return c;`
	jit, slow := compilePair(t, rt, src)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		rec = rec[:0]
		got, err := fn.Exec[bool](map[string]any{"rec": &rec})
		if err != nil || got || strings.Join(rec, "") != "L" {
			t.Errorf("%s: got %v, rec %v, err %v", name, got, rec, err)
		}
	}
}
