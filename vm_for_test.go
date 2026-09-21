package gozero

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// errForBoom is what a condition call fails with, so both tiers
// report the same error from the same place.
var errForBoom = errors.New("boom")

// forRuntime is rangeRuntime plus the sources the condition and
// three-clause forms drive: a draining budget, a gated struct, a
// jumping counter, and the fixed-width integers the wrap cases start
// from.
func forRuntime(t testing.TB) (*Runtime, *[]string) {
	t.Helper()
	rt, seen := rangeRuntime(t)
	bind := map[string]any{
		// budget's More consumes one credit per call: the loop's own
		// state lives in a value the program built, so both tiers of
		// an equivalence test start fresh.
		"budget": newForBudget,
		// mk and gated take no argument, a shape the direct tier
		// calls, for the Supports checks.
		"mk":    func() *forBudget { return &forBudget{left: 2} },
		"gated": func() *forGate { return &forGate{Open: true, left: 2} },
		// gate's Open field is the condition; Tick closes it after n.
		"gate": newForGate,
		// bump jumps a counter forward, which is the only way a body
		// moves the loop variable other than a step statement.
		"bump": func(n int64) int64 { return n + 2 },
		// The fixed-width starts for the wrap cases.
		"u8v": func() uint8 { return 250 },
		"i8v": func() int8 { return 126 },
		// boom errors from condition position.
		"boom": func() (bool, error) { return false, errForBoom },
		// wrap nests a struct two fields deep, a chain of added
		// offsets the direct tier reads.
		"wrap": func() *forWrap { return &forWrap{} },
		// embed promotes the same field through an embedded struct,
		// whose two-step index the direct tier declines.
		"embed": func() *forEmbed { return &forEmbed{} },
	}
	for name, fn := range bind {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	return rt, seen
}

type forBudget struct{ left int64 }

// More consumes one credit and reports whether one remained.
func (b *forBudget) More() bool {
	b.left--
	return b.left >= 0
}

func newForBudget(n int64) *forBudget { return &forBudget{left: n} }

type forGate struct {
	Open bool
	left int64
}

// Tick counts down and closes the gate at zero.
func (g *forGate) Tick() error {
	g.left--
	if g.left <= 0 {
		g.Open = false
	}
	return nil
}

func newForGate(n int64) *forGate { return &forGate{Open: n > 0, left: n} }

type forInner struct{ OK bool }

type forWrap struct{ In forInner }

type forEmbed struct{ forInner }

// TestForCompileErrors pins the named rules of the loop headers: the
// condition is a static bool, the loop variable an integer, the
// comparison the same closed operand set an if header has, and a
// body cannot retype a header name.
func TestForCompileErrors(t *testing.T) {
	rt, _ := forRuntime(t)
	for name, tc := range map[string]struct{ src, want string }{
		"cond not bool":    {`for poke() { }`, "the loop condition must be bool, poke returns *url.URL"},
		"cond stack name":  {`for zz { poke() }`, "the loop condition zz is not a name bound by the program"},
		"cond string name": {`s := join("a"); for s { poke() }`, "the loop condition must be bool, s is string"},
		"init float":       {`for i := 1.5; i < 3; i++ { }`, "a loop variable's init must be an integer"},
		"init string call": {`for i := join("a"); i < 3; i++ { }`, "the loop variable i is string"},
		"init stack name":  {`for i := zz; i < 3; i++ { }`, "not a name bound by the program"},
		"operand slice":    {`parts := fields("a b"); for i := 0; i < parts; i++ { }`, "counts with an integer"},
		"operand stack":    {`for i := 0; i < zz; i++ { }`, "not a name bound by the program"},
		"mixed widths":     {`u := u8v(); n := count(); for i := 0; u < n; i++ { }`, "mismatched types uint8 and int"},
		"negative unsign":  {`u := u8v(); for i := u; u < -1; i++ { }`, "identical types"},
		"var retyped":      {`for i := 0; i < 3; i++ { i = poke() }`, "the loop variable i is reassigned as *url.URL inside the loop body"},
		"operand retyped":  {`n := count(); for i := 0; i < n; i++ { n = poke() }`, "the comparison operand n is reassigned as *url.URL inside the loop body"},
		"cond retyped":     {`b := mk(); ok := b.More(); for ok { ok = poke() }`, "the loop condition ok is reassigned as *url.URL inside the loop body"},
	} {
		_, err := rt.Compile(tc.src)
		if err == nil {
			t.Errorf("%s: compiled, want an error naming %q", name, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %q, want it to name %q", name, err, tc.want)
		}
	}
}

// TestForReflectTier runs a header the direct tier declines, so the
// reflect evaluator's own loop executes it: a condition promoted
// through an embedded struct, outside the single-field rule.
func TestForReflectTier(t *testing.T) {
	rt, seen := forRuntime(t)
	src := `
		w := embed();
		for w.OK {
			poke();
		}
		poke();
	`
	err := rt.Supports(src)
	if err == nil {
		t.Fatal("a promoted condition should decline the direct tier")
	}
	if !strings.Contains(err.Error(), "only a single field is in the table") {
		t.Errorf("the reason does not name the rule: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	*seen = nil
	if _, err := fn.ExecContext[any](context.Background(), nil); err != nil {
		t.Errorf("the reflect evaluator should run it: %v", err)
	}
	if len(*seen) != 1 {
		t.Errorf("a closed gate should loop zero times, saw %v", *seen)
	}
}
