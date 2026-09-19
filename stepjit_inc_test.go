package gozero

import (
	"context"
	"fmt"
	"testing"
)

// TestStepJITIncDecMatchesReflect runs the shared step table from
// vm_inc_test.go down both tiers and requires identical results:
// every scalar class, every wraparound case.
func TestStepJITIncDecMatchesReflect(t *testing.T) {
	rt := incRuntime(t)
	for name, tc := range incPrograms {
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

// TestIncDecSupports proves a step is a direct-tier statement: a
// program of declarations, steps and one direct call reports no
// reason to decline.
func TestIncDecSupports(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("sink", func(int32) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Supports(`n := int32(1); n++; n--; n++; sink(n);`); err != nil {
		t.Fatalf("a step program should JIT: %v", err)
	}
}

// TestIncDecZeroAlloc pins the cost of the step itself: a JIT
// program that steps every scalar class and hands the results to
// direct calls allocates nothing per run.
func TestIncDecZeroAlloc(t *testing.T) {
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"sink32": func(int32) error { return nil },
		"sink8":  func(uint8) error { return nil },
		"sinkF":  func(float64) error { return nil },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	src := `
		n := int32(1); n++; n--; n++;
		var b uint8; b--;
		x := 2.5; x++;
		sink32(n); sink8(b); sinkF(x);
	`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("the step program should JIT: %v", err)
	}
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
		t.Fatalf("a step program allocates %.2f per run, want 0", allocs)
	}
}

// TestIncDecJITSharedRuns mirrors TestIncDecSharedLiteral on the
// direct tier: two runs of one compiled program both start from the
// literal, not from the previous run's frame.
func TestIncDecJITSharedRuns(t *testing.T) {
	rt := incRuntime(t)
	jit, _ := compilePair(t, rt, `n := 5; n++; s := fmt.Sprintf("%d", n); return s;`)
	for run := 0; run < 2; run++ {
		got, err := jit(context.Background(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != "6" {
			t.Fatalf("run %d: got %v, want 6", run, got)
		}
	}
}

// TestIncDecAliasingUnaffected pins where the writes gate stands: the
// interface-aliasing optimization only ever covers non-scalar slots,
// and a step only compiles at a scalar, so no program loses aliasing
// to a step. The stepped scalar still boxes through the same path a
// once-written scalar does, staticBoxes for small values.
func TestIncDecAliasingUnaffected(t *testing.T) {
	rt := incRuntime(t)
	var seen string
	if err := rt.Bind("record", func(v any) { seen = fmt.Sprintf("%T:%v", v, v) }); err != nil {
		t.Fatal(err)
	}
	jit, slow := compilePair(t, rt, `n := int32(41); n++; record(n);`)
	if _, err := jit(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	jitSeen := seen
	if _, err := slow(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if jitSeen != seen {
		t.Fatalf("callee saw %s (jit) vs %s (reflect)", jitSeen, seen)
	}
	if jitSeen != "int32:42" {
		t.Fatalf("callee saw %s, want int32:42", jitSeen)
	}
}
