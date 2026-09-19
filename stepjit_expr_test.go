package gozero

import (
	"context"
	"strings"
	"testing"
)

// TestExprSupports proves the full grammar is a direct-tier surface:
// precedence, parentheses, unary operators, logic and a shift all
// compile to closures with no reflect bridge.
func TestExprSupports(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("sink", func(bool) error { return nil }); err != nil {
		t.Fatal(err)
	}
	src := `
		n := 7; total := n*3 + 1;
		grouped := (n + 3) * 2;
		neg := -total + grouped;
		ok := total > 20 && neg <= 0 || !true;
		bits := n&3 | n<<2;
		hit := ok == (bits != 0);
		sink(hit);
	`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("the expression program should JIT: %v", err)
	}
}

// TestExprScalarZeroAlloc pins the cost of the tree itself: a JIT
// program of arithmetic, comparisons, logic, shifts and unary
// operators keeps every value in its class, so nothing allocates per
// run.
func TestExprScalarZeroAlloc(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("sink", func(bool) error { return nil }); err != nil {
		t.Fatal(err)
	}
	src := `
		n := 40; m := n*2 + n/4 - 3;
		var w uint8; w = 200; z := w*2 + 1;
		f := 1.5; g := (f + 0.5) * 2.0;
		ok := m > 80 && z == 145 || g < 0.0;
		hit := ok != (-n + 41 == 1);
		no := !hit;
		sink(no);
	`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("the expression program should JIT: %v", err)
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
		t.Fatalf("a scalar expression program allocates %.2f per run, want 0", allocs)
	}
}

// concatChainOperands lives outside the closures below so the Go
// compiler cannot fold the native chain into a constant.
var concatChainOperands = struct{ a, b, c string }{"go", "-", "zero"}

// TestConcatChainAllocParity proves a chained concat allocates
// exactly where compiled Go allocates: once for the result, not once
// per + node, because the direct tier flattens the chain the way the
// compiler lowers it to one concatstrings call.
func TestConcatChainAllocParity(t *testing.T) {
	rt := NewRuntime()
	src := `a := "go"; b := "-"; c := "zero"; s := a + b + c + b + a;`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("the concat chain should JIT: %v", err)
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
		o := &concatChainOperands
		sink = o.a + o.b + o.c + o.b + o.a
	})
	_ = sink
	if vm != native {
		t.Fatalf("chained concat allocates %.2f per run on the JIT, %.2f in compiled Go", vm, native)
	}
	if vm != 1 {
		t.Fatalf("chained concat allocates %.2f per run, want exactly the result's bytes", vm)
	}

	// The value matches too, and a chain with one non-empty part
	// returns it without building, as the runtime does.
	fn, err := rt.Compile(src + ` return s;`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fn(ctx, nil, nil)
	if err != nil || got != "go-zero-go" {
		t.Fatalf("chain value: %v, %v", got, err)
	}
	one, err := rt.Compile(`a := "solo"; b := ""; s := b + a + b + b; return s;`)
	if err != nil {
		t.Fatal(err)
	}
	got, err = one(ctx, nil, nil)
	if err != nil || got != "solo" {
		t.Fatalf("single part chain: %v, %v", got, err)
	}
}

// TestExprJITShortCircuit proves laziness by effect on the direct
// tier: the guarded division never runs, the unguarded one panics
// with the runtime's own message through the guard.
func TestExprJITShortCircuit(t *testing.T) {
	rt := NewRuntime()
	jit, _ := compilePair(t, rt, `n := 0; ok := n != 0 && 7/n > 1; return ok;`)
	got, err := guard(jit)(context.Background(), nil, nil)
	if err != nil || got != false {
		t.Fatalf("guarded division: got %v, %v", got, err)
	}
	jit, _ = compilePair(t, rt, `n := 0; ok := n == 0 && 7/n > 1; return ok;`)
	if _, err := guard(jit)(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "integer divide by zero") {
		t.Fatalf("unguarded division: err = %v", err)
	}
}

// TestExprRebindsOwnName covers the accumulator shape at depth: the
// slot rewrites through its own expression, and a rerun starts from
// the source values, not the previous run's.
func TestExprRebindsOwnName(t *testing.T) {
	rt := NewRuntime()
	fn, err := rt.Compile(`n := 1; n = n*2 + 1; n = n*2 + 1; return n;`)
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 2; run++ {
		got, err := fn(context.Background(), nil, nil)
		if err != nil || got != int64(7) {
			t.Fatalf("run %d: got %v, %v", run, got, err)
		}
	}
}
