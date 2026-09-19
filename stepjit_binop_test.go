package gozero

import (
	"context"
	"fmt"
	"testing"
)

// TestStepJITBinopMatchesReflect runs the shared oracle table from
// vm_binop_test.go down both tiers and requires identical results:
// concat, every wraparound case, the float edges and the
// comparisons. The table's wants come from compiled Go, so the two
// tiers cannot agree on a shared misunderstanding.
func TestStepJITBinopMatchesReflect(t *testing.T) {
	rt := binopRuntime(t)
	for name, tc := range binopPrograms {
		jit, slow := compilePair(t, rt, tc.src)
		jitRes, jitErr := jit(context.Background(), nil, nil)
		slowRes, slowErr := slow(context.Background(), nil, nil)
		if (jitErr == nil) != (slowErr == nil) {
			t.Errorf("%s: err = %v (jit) vs %v (reflect)", name, jitErr, slowErr)
			continue
		}
		if jitRes != slowRes {
			t.Errorf("%s: result = %v (jit) vs %v (reflect)", name, jitRes, slowRes)
		}
		if jitRes != tc.want {
			t.Errorf("%s: got %v, want %s", name, jitRes, tc.want)
		}
	}
}

// TestBinopCalleeSeesSame compares the observable effect, not just
// the return value: a callee handed the operator's result through an
// any parameter sees the same dynamic type and value on both tiers,
// whether the slot aliases the frame (written once) or copies
// (rewritten).
func TestBinopCalleeSeesSame(t *testing.T) {
	for name, src := range map[string]string{
		"write-once concat":  `a := "left"; b := "right"; s := a + b; record(s);`,
		"rewritten concat":   `a := "left"; b := "right"; s := a + b; s = s + a; record(s);`,
		"comparison result":  `n := 41; ok := n == 41; record(ok);`,
		"wrapped scalar sum": `var w uint8; w = 255; z := w + 1; record(z);`,
	} {
		rt := binopRuntime(t)
		var seen string
		if err := rt.Bind("record", func(v any) { seen = fmt.Sprintf("%T:%v", v, v) }); err != nil {
			t.Fatal(err)
		}
		jit, slow := compilePair(t, rt, src)
		if _, err := jit(context.Background(), nil, nil); err != nil {
			t.Fatal(err)
		}
		jitSeen := seen
		if _, err := slow(context.Background(), nil, nil); err != nil {
			t.Fatal(err)
		}
		if jitSeen != seen {
			t.Errorf("%s: callee saw %s (jit) vs %s (reflect)", name, jitSeen, seen)
		}
	}
}

// TestBinopSupports proves an operator assignment is a direct-tier
// statement: a program of literal assignments, operators and direct
// calls reports no reason to decline.
func TestBinopSupports(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("sink", func(bool) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Supports(`a := "x"; s := a + "y"; ok := s == "xy"; hit := ok != false; sink(hit);`); err != nil {
		t.Fatalf("an operator program should JIT: %v", err)
	}
}

// TestBinopScalarZeroAlloc pins the cost of the operators
// themselves: a JIT program of integer adds and comparisons keeps
// every value in its class, so nothing allocates per run.
func TestBinopScalarZeroAlloc(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("sink", func(bool) error { return nil }); err != nil {
		t.Fatal(err)
	}
	src := `
		n := 40; m := n + 2;
		var w uint8; w = 255; z := w + 1;
		ok := m == 42; zz := z == 0; hit := ok == zz;
		sink(hit);
	`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("the operator program should JIT: %v", err)
	}
	jp := jitProgramOf(t, rt, src)
	ctx := context.Background()
	if _, err := jp.run(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(200, func() {
		if _, err := jp.run(ctx, nil, nil); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("a scalar operator program allocates %.2f per run, want 0", allocs)
	}
}

// concatOperands lives outside the closures below so the Go compiler
// cannot fold the native concatenation into a constant.
var concatOperands = struct{ a, b string }{"left-", "right"}

// TestConcatAllocParity proves s := a + b allocates exactly where
// compiled Go allocates: once, for the result's bytes. The program's
// frame is pooled, so the concat is the only allocation per run, and
// the native side is the same statement over the same values.
func TestConcatAllocParity(t *testing.T) {
	rt := NewRuntime()
	src := `a := "left-"; b := "right"; s := a + b;`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("the concat program should JIT: %v", err)
	}
	jp := jitProgramOf(t, rt, src)
	ctx := context.Background()
	if _, err := jp.run(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	vm := testing.AllocsPerRun(200, func() {
		if _, err := jp.run(ctx, nil, nil); err != nil {
			t.Fatal(err)
		}
	})
	var sink string
	native := testing.AllocsPerRun(200, func() {
		sink = concatOperands.a + concatOperands.b
	})
	_ = sink
	if vm != native {
		t.Fatalf("concat allocates %.2f per run on the JIT, %.2f in compiled Go", vm, native)
	}
	if vm != 1 {
		t.Fatalf("concat allocates %.2f per run, want exactly the result's bytes", vm)
	}
}

// TestBinopAliasingCost pins where the writes gate moves the
// allocations. A concat result read through an any parameter either
// aliases the frame (written once: no box, but the frame escapes and
// cannot pool) or copies into a box (rewritten: one box per read,
// frame pooled). The four programs pin all four corners:
//
//	write-once + sink: concat 1 + unpooled frame 1     = 2
//	rewritten  + sink: concat 2 + box 1 (frame pooled) = 3
//	write-once, no sink: concat 1 (frame pooled)       = 1
//	rewritten,  no sink: concat 2 (frame pooled)       = 2
//
// The rewrite itself never costs more than its own concat; what a
// second write changes is the shape of the interface read: a box
// instead of an escaped frame.
func TestBinopAliasingCost(t *testing.T) {
	for name, tc := range map[string]struct {
		src    string
		allocs float64
		pooled bool
	}{
		"write-once to any": {`a := "left-"; b := "right"; s := a + b; sink(s);`, 2, false},
		"rewritten to any":  {`a := "left-"; b := "right"; s := a + b; s = s + a; sink(s);`, 3, true},
		"write-once unread": {`a := "left-"; b := "right"; s := a + b;`, 1, true},
		"rewritten unread":  {`a := "left-"; b := "right"; s := a + b; s = s + a;`, 2, true},
	} {
		rt := NewRuntime()
		if err := rt.Bind("sink", func(v any) *int { return nil }); err != nil {
			t.Fatal(err)
		}
		if err := rt.Supports(tc.src); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		jp := jitProgramOf(t, rt, tc.src)
		ctx := context.Background()
		if _, err := jp.run(ctx, nil, nil); err != nil {
			t.Fatal(err)
		}
		if (jp.pool != nil) != tc.pooled {
			t.Errorf("%s: pool=%v, want %v", name, jp.pool != nil, tc.pooled)
		}
		allocs := testing.AllocsPerRun(500, func() {
			if _, err := jp.run(ctx, nil, nil); err != nil {
				t.Fatal(err)
			}
		})
		if allocs != tc.allocs {
			t.Errorf("%s: %.2f allocs/run, want %.2f", name, allocs, tc.allocs)
		}
	}
}

// jitProgramOf compiles src straight to the direct tier, failing the
// test when it declines.
func jitProgramOf(t *testing.T, rt *Runtime, src string) *jitProgram {
	t.Helper()
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	p, err := rt.compiler.compileProgram(prog)
	if err != nil {
		t.Fatal(err)
	}
	jp, err := jitCompileProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	return jp
}
