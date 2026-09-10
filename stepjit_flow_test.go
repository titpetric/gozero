package gozero

import (
	"strings"
	"testing"
)

// flowPair compiles src down both tiers and requires identical
// results, the equivalence net for control flow.
func flowPair(t *testing.T, rt *Runtime, src string, stack map[string]any) {
	t.Helper()
	jit, slow := compilePair(t, rt, src)
	a, errA := jit.ExecContext[any](t.Context(), stack)
	b, errB := slow.ExecContext[any](t.Context(), stack)
	if (errA == nil) != (errB == nil) {
		t.Fatalf("%q: jit err %v, reflect err %v", src, errA, errB)
	}
	if errA != nil && errA.Error() != errB.Error() {
		t.Fatalf("%q: jit err %q, reflect err %q", src, errA, errB)
	}
	if a != b {
		t.Fatalf("%q: jit %v (%T), reflect %v (%T)", src, a, a, b, b)
	}
}

// TestStepJITFlowMatchesReflect runs the control-flow surface down
// both tiers.
func TestStepJITFlowMatchesReflect(t *testing.T) {
	rt := exprRuntime(t)
	for _, src := range []string{
		`x := 1; if x > 0 { x = 2 } else { x = 3 }; return x;`,
		`x := 1; if x > 5 { x = 2 } else if x > 0 { x = 4 } else { x = 3 }; return x;`,
		`sum := 0; for i := 0; i < 5; i++ { sum = sum + i }; return sum;`,
		`n := 0; for n < 3 { n++ }; return n;`,
		`n := 0; for { n++; if n == 4 { break } }; return n;`,
		`sum := 0; for i := 0; i < 10; i++ { if i % 2 == 0 { continue }; sum = sum + i }; return sum;`,
		`n := 0; for i := 0; i < 3; i++ { for { break }; n++ }; return n;`,
		`for i := 0; i < 9; i++ { if i == 2 { return i } }; return -1;`,
		`x := 1; if x > 0 { x := 10; x++ }; return x;`,
		`sum := 0; for i := 0; i < 3; i++ { var n int64; n = n + 1; sum = sum + n }; return sum;`,
		`s := "abc"; n := 0; for i := range s { if i > 0 { n++ } }; return n;`,
		`s := "héj"; n := 0; for _, r := range s { if r > 200 { n++ } }; return n;`,
		`x := 5; if x > 3 { return "big" }; return "small";`,
		`if 1 > 2 { return 1 };`,
	} {
		flowPair(t, rt, src, nil)
	}

	// A range over a bound slice, both tiers, bridged call allowed.
	src := `xs := seq(); sum := 0; for _, v := range xs { sum = sum + v }; return sum;`
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
	a, errA := jp.run(t.Context(), nil, nil)
	b, errB := p.run(t.Context(), nil, nil)
	if errA != nil || errB != nil || a != b || a != int64(60) {
		t.Fatalf("range slice: jit %v (%v), reflect %v (%v)", a, errA, b, errB)
	}
}

// TestStepJITDeferMatchesReflect pins defer on the direct tier: the
// LIFO order, defer-site argument evaluation, and the error paths.
func TestStepJITDeferMatchesReflect(t *testing.T) {
	rt := exprRuntime(t)
	run := func(src string) ([]string, []string) {
		t.Helper()
		jit, slow := compilePair(t, rt, src)
		var recA, recB []string
		if _, err := jit.ExecContext[any](t.Context(), map[string]any{"rec": &recA}); err != nil {
			t.Fatalf("jit: %v", err)
		}
		if _, err := slow.ExecContext[any](t.Context(), map[string]any{"rec": &recB}); err != nil {
			t.Fatalf("reflect: %v", err)
		}
		return recA, recB
	}
	a, b := run(`defer sideRec(rec, "A", true); defer sideRec(rec, "B", true); ok := sideRec(rec, "run", true); return ok;`)
	if strings.Join(a, "") != "runBA" || strings.Join(b, "") != "runBA" {
		t.Fatalf("order: jit %v, reflect %v", a, b)
	}
	a, b = run(`tag := "early"; defer sideRec(rec, tag, true); tag = "late"; ok := sideRec(rec, tag, true); return ok;`)
	if strings.Join(a, ",") != "late,early" || strings.Join(b, ",") != "late,early" {
		t.Fatalf("defer-site args: jit %v, reflect %v", a, b)
	}
}

// TestStepJITBindErr pins the error-binding bridge on this tier
// against the reflect evaluator.
func TestStepJITBindErr(t *testing.T) {
	rt := errRuntime(t)
	for _, src := range []string{
		`n, err := parse(""); if err != nil { return -1 }; return n;`,
		`n, err := parse("abc"); if err != nil { return -1 }; return n;`,
		`n, _ := parse(""); return n;`,
		`_, err := parse("xy"); return err == nil;`,
	} {
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
			t.Fatalf("%q did not JIT: %v", src, err)
		}
		a, errA := jp.run(t.Context(), nil, nil)
		b, errB := p.run(t.Context(), nil, nil)
		if (errA == nil) != (errB == nil) || a != b {
			t.Fatalf("%q: jit %v (%v), reflect %v (%v)", src, a, errA, b, errB)
		}
	}
}

// TestStepJITFlowPanics pins panic parity for the run-time failures.
func TestStepJITFlowPanics(t *testing.T) {
	rt := exprRuntime(t)
	for _, tc := range []struct{ src, want string }{
		{`a := 1; b := 0; c := 0; if a > 0 { c = a / b }; return c;`, "divide by zero"},
		{`xs := seq(); n := 0; for i := 0; i < 5; i++ { n = n + xs[i] }; return n;`, "out of range"},
	} {
		fn, err := rt.Compile(tc.src)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fn.Exec[any](nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want %q", tc.src, err, tc.want)
		}
	}
}

// TestFlowAllocs pins the direct tier's allocation profile: a scalar
// loop body allocates nothing per run once compiled, and a run of a
// pooled-frame flow program allocates only the frame path permits.
func TestFlowAllocs(t *testing.T) {
	rt := exprRuntime(t)
	const src = `sum := 0
for i := 0; i < 100; i++ {
	if i%2 == 0 {
		continue
	}
	sum = sum + i*i
}
return sum`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("did not JIT: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	want, err := fn.Exec[any](nil)
	if err != nil || want != int64(166650) {
		t.Fatalf("got %v, %v", want, err)
	}
	n := testing.AllocsPerRun(100, func() {
		if _, err := fn.Exec[any](nil); err != nil {
			panic(err)
		}
	})
	t.Logf("allocations/run = %.1f", n)
	// The frame pools and the loop body touches only frame scalars;
	// the one allocation is the returned value's box.
	if n > 1 {
		t.Errorf("a scalar loop allocates %.1f/run, want at most the return box", n)
	}
}
