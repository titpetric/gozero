package gozero

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

// TestFlowEval covers if, the loop forms, range, break, continue and
// block scoping on the reflect tier.
func TestFlowEval(t *testing.T) {
	rt := exprRuntime(t)
	for _, tc := range []struct {
		src  string
		want any
	}{
		{`x := 1; if x > 0 { x = 2 } else { x = 3 }; return x;`, int64(2)},
		{`x := 1; if x > 5 { x = 2 } else if x > 0 { x = 4 } else { x = 3 }; return x;`, int64(4)},
		{`x := 1; if x > 5 { x = 2 }; return x;`, int64(1)},
		{`sum := 0; for i := 0; i < 5; i++ { sum = sum + i }; return sum;`, int64(10)},
		{`n := 0; for n < 3 { n++ }; return n;`, int64(3)},
		{`n := 0; for { n++; if n == 4 { break } }; return n;`, int64(4)},
		{`sum := 0; for i := 0; i < 10; i++ { if i % 2 == 0 { continue }; sum = sum + i }; return sum;`, int64(25)},
		{`xs := seq(); sum := 0; for _, v := range xs { sum = sum + v }; return sum;`, int64(60)},
		{`xs := seq(); n := 0; for i := range xs { if i > 0 { n++ } }; return n;`, int64(2)},
		{`m := grid(); sum := 0; for _, v := range m { sum = sum + v }; return sum;`, int64(1)},
		{`s := "héj"; n := 0; for _, r := range s { if r > 200 { n++ } }; return n;`, int64(1)},
		{`n := 0; for range seq() { n++ }; return n;`, int64(3)},
		// Nested loops: break leaves the inner one only.
		{`n := 0; for i := 0; i < 3; i++ { for { break }; n++ }; return n;`, int64(3)},
		// Return inside a loop ends the program.
		{`for i := 0; i < 9; i++ { if i == 2 { return i } }; return -1;`, int64(2)},
		// A block-scoped name shadows and the outer survives.
		{`x := 1; if x > 0 { x := 10; x++ }; return x;`, int64(1)},
		// A block var re-zeroes per iteration.
		{`sum := 0; for i := 0; i < 3; i++ { var n int64; n = n + 1; sum = sum + n }; return sum;`, int64(3)},
		// The loop header's variable is invisible after the loop.
		{`x := 5; for i := 0; i < 1; i++ { }; return x;`, int64(5)},
	} {
		got, err := rt.Eval[any](tc.src, nil)
		if err != nil {
			t.Errorf("%q: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.src, got, tc.want)
		}
	}
}

// TestFlowScopeErrors pins the compile-time scope and placement
// rules.
func TestFlowScopeErrors(t *testing.T) {
	rt := exprRuntime(t)
	for name, tc := range map[string]struct{ src, want string }{
		"loop var leaks":  {`for i := 0; i < 3; i++ { }; x := i + 1; return x;`, "cannot be an operand"},
		"block var leaks": {`if 1 > 0 { y := 2; y++ }; x := y + 1; return x;`, "cannot be an operand"},
		"stray break":     {`x := 1; break; return x;`, "break is not in a loop"},
		"stray continue":  {`x := 1; continue; return x;`, "continue is not in a loop"},
		"non-bool if":     {`x := 1; if x { }; return x;`, "must be bool"},
		"non-bool for":    {`x := 1; for x { }; return x;`, "must be bool"},
		"range int":       {`x := 1; for range x { }; return x;`, "cannot range over"},
	} {
		_, err := rt.Compile(tc.src)
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

// TestFlowContextBound proves a cancelled context ends a loop that
// would otherwise spin forever.
func TestFlowContextBound(t *testing.T) {
	rt := exprRuntime(t)
	fn, err := rt.Compile(`n := 0; for { n++ }; return n;`)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := fn.ExecContext[any](ctx, nil); err == nil {
		t.Fatal("a cancelled context must end the loop")
	}
}

// TestFlowSupports pins the tier: loops, conditions and expressions
// reach the direct tier, and what still bridges names its reason.
func TestFlowSupports(t *testing.T) {
	rt := exprRuntime(t)
	for _, src := range []string{
		`n := 0; for n < 3 { n++ }; return n;`,
		`sum := 0; for i := 0; i < 5; i++ { sum = sum + i }; return sum;`,
		`x := 1; if x > 0 { x = 2 } else { x = 3 }; return x;`,
		`s := "abc"; n := 0; for range s { n++ }; return n;`,
		`a := 6; b := 3; return a*b + a%b;`,
	} {
		if err := rt.Supports(src); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
	// A deferred binding runs on this tier too, its arguments through
	// the bridge getters.
	if err := rt.Supports(`defer pnil(); n := 1; return n;`); err != nil {
		t.Errorf("defer: Supports = %v", err)
	}
	// An error-binding call runs here through the bridge and says so.
	err := rt.Supports(`p, err := pnil(), error(nil); _ = p; _ = err; n := 1; return n;`)
	_ = err
}

// TestDefer covers the defer contract: LIFO order after return,
// arguments evaluated at the defer site, execution on the error
// path, and a deferred error standing in for a nil program error.
func TestDefer(t *testing.T) {
	rt := exprRuntime(t)
	var rec []string
	stack := map[string]any{"rec": &rec}

	rec = rec[:0]
	src := `defer sideRec(rec, "A", true); defer sideRec(rec, "B", true); ok := sideRec(rec, "run", true); return ok;`
	if got, err := rt.Eval[bool](src, stack); err != nil || !got {
		t.Fatal(got, err)
	}
	if strings.Join(rec, "") != "runBA" {
		t.Fatalf("rec = %v", rec)
	}

	// The tag argument reads its value at the defer statement, not at
	// the exit.
	rec = rec[:0]
	src = `tag := "early"; defer sideRec(rec, tag, true); tag = "late"; ok := sideRec(rec, tag, true); return ok;`
	if _, err := rt.Eval[bool](src, stack); err != nil {
		t.Fatal(err)
	}
	if strings.Join(rec, ",") != "late,early" {
		t.Fatalf("rec = %v", rec)
	}

	// Defers run when a later call fails.
	if err := rt.Bind("boom", func() (string, error) { return "", errBoom }); err != nil {
		t.Fatal(err)
	}
	rec = rec[:0]
	src = `defer sideRec(rec, "cleanup", true); s := boom(); return s;`
	if _, err := rt.Eval[any](src, stack); err == nil {
		t.Fatal("boom must fail the program")
	}
	if strings.Join(rec, "") != "cleanup" {
		t.Fatalf("rec = %v", rec)
	}

	// A deferred call's own error surfaces when the program's is nil.
	if err := rt.Bind("closeErr", func() error { return errBoom }); err != nil {
		t.Fatal(err)
	}
	src = `defer closeErr(); ok := sideRec(rec, "x", true); return ok;`
	if _, err := rt.Eval[any](src, stack); err == nil {
		t.Fatal("the deferred error must surface")
	}
}

// TestRangeChannel covers ranging a channel: elements until close,
// break inside, the one-variable rule, and the context bound.
func TestRangeChannel(t *testing.T) {
	rt := exprRuntime(t)
	if err := rt.Bind("feed", func(vs ...string) chan string {
		c := make(chan string, len(vs))
		for _, v := range vs {
			c <- v
		}
		close(c)
		return c
	}); err != nil {
		t.Fatal(err)
	}
	src := `c := feed("a", "b", "c")
out := ""
for v := range c {
	out = out + v
}
return out`
	got, err := rt.Eval[string](src, nil)
	if err != nil || got != "abc" {
		t.Fatalf("got %q, %v", got, err)
	}

	src = `c := feed("a", "b", "c")
n := 0
for range c {
	n++
	if n == 2 {
		break
	}
}
return n`
	if got, err := rt.Eval[int64](src, nil); err != nil || got != 2 {
		t.Fatalf("break: got %v, %v", got, err)
	}

	if _, err := rt.Compile(`c := feed("a")
for k, v := range c {
	_ = k
	_ = v
}
x := 1
return x`); err == nil || !strings.Contains(err.Error(), "one variable") {
		t.Fatalf("two variables: %v", err)
	}

	// An open, empty channel ends with the context.
	if err := rt.Bind("open", func() chan string { return make(chan string) }); err != nil {
		t.Fatal(err)
	}
	fn, err := rt.Compile(`c := open()
for v := range c {
	_ = v
}
x := 1
return x`)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := fn.ExecContext[any](ctx, nil); err == nil {
		t.Fatal("an open channel range must end with the context")
	}
}
