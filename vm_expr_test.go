package gozero

import (
	"errors"
	"strings"
	"testing"
)

func exprRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"i8":      func(n int8) int8 { return n },
		"u8":      func(n uint8) uint8 { return n },
		"i32":     func(n int32) int32 { return n },
		"f32":     func(f float32) float32 { return f },
		"fields":  strings.Fields,
		"seq":     func() []int64 { return []int64{10, 20, 30} },
		"grid":    func() map[string]int64 { return map[string]int64{"a": 1} },
		"pnil":    func() *strings.Reader { return nil },
		"pval":    func() *strings.Reader { return strings.NewReader("x") },
		"sideRec": func(rec *[]string, tag string, v bool) bool { *rec = append(*rec, tag); return v },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	return rt
}

// TestExprEval covers the operator surface on the reflect tier:
// widths, adoption, comparisons, logic, strings, indexing and len.
func TestExprEval(t *testing.T) {
	rt := exprRuntime(t)
	for _, tc := range []struct {
		src  string
		want any
	}{
		{`x := 2 + 3*4; return x;`, int64(14)},                      // folded
		{`a := 2; b := 3; return a*b + 1;`, int64(7)},               // slots
		{`n := i8(100); n = n + n; return n;`, int8(-56)},           // wraparound at width
		{`n := u8(200); n = n + 100; return n;`, uint8(44)},         // unsigned wrap
		{`n := i32(-7); return n % 3;`, int32(-1)},                  // signed modulo
		{`n := i32(-7); return n / 2;`, int32(-3)},                  // truncated division
		{`n := i8(-128); m := i8(-1); return n / m;`, int8(-128)},   // overflow edge
		{`f := 1.5; return f * 2.0;`, float64(3)},                   //
		{`f := f32(0.1); return f + f32(0.2);`, float32(0.3)},       // rounded at 32 bits
		{`a := 5; return a<<2 + 1;`, int64(21)},                     // << binds tighter than +
		{`n := i8(1); return n << 10;`, int8(0)},                    // shift past width
		{`a := 6; b := 3; return a &^ b;`, int64(4)},                //
		{`s := "b"; return "a" < s;`, true},                         //
		{`s := "x"; return s + "y" + "z";`, "xyz"},                  //
		{`ok := false; return !ok;`, true},                          //
		{`a := 1; b := 2; return a < b && b < 3;`, true},            //
		{`xs := seq(); return xs[1];`, int64(20)},                   //
		{`xs := seq(); return xs[1] + xs[2];`, int64(50)},           //
		{`m := grid(); return m["a"];`, int64(1)},                   //
		{`m := grid(); return m["missing"];`, int64(0)},             // zero for a missing key
		{`s := "abc"; return s[1];`, byte('b')},                     //
		{`xs := seq(); return len(xs);`, 3},                         //
		{`s := "abcd"; n := len(s); return n;`, 4},                  //
		{`ws := fields("a b c"); return len(ws);`, 3},               //
		{`p := pnil(); return p == nil;`, true},                     //
		{`p := pval(); return p != nil;`, true},                     //
		{`n := -5; return -n;`, int64(5)},                           //
		{`n := i8(3); return ^n;`, int8(-4)},                        //
		{`xs := seq(); return xs[len(xs)-1];`, int64(30)},           //
		{`a := 7; return (a & 1) == 1;`, true},                      //
	} {
		got, err := rt.Eval[any](tc.src, nil)
		if err != nil {
			t.Errorf("%q: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %v (%T), want %v (%T)", tc.src, got, got, tc.want, tc.want)
		}
	}
}

// TestExprShortCircuit proves the right side does not run when the
// left decides, with a side-effecting probe.
func TestExprShortCircuit(t *testing.T) {
	rt := exprRuntime(t)
	var rec []string
	stack := map[string]any{"rec": &rec}
	src := `a := sideRec(rec, "L", false); b := sideRec(rec, "R", true); c := a && b; return c;`
	if _, err := rt.Eval[any](src, stack); err != nil {
		t.Fatal(err)
	}
	// Both statements run: the calls are statements, not the operator.
	if strings.Join(rec, "") != "LR" {
		t.Fatalf("rec = %v", rec)
	}

	rec = rec[:0]
	src = `c := sideRec(rec, "L", false) && sideRec(rec, "R", true); return c;`
	got, err := rt.Eval[bool](src, stack)
	if err != nil {
		t.Fatal(err)
	}
	if got || strings.Join(rec, "") != "L" {
		t.Fatalf("got %v, rec = %v", got, rec)
	}

	rec = rec[:0]
	src = `c := sideRec(rec, "L", true) || sideRec(rec, "R", true); return c;`
	if got, err := rt.Eval[bool](src, stack); err != nil || !got {
		t.Fatal(got, err)
	}
	if strings.Join(rec, "") != "L" {
		t.Fatalf("rec = %v", rec)
	}
}

// TestExprPanics pins the run-time failures: division by zero and an
// index out of range arrive as *PanicError, as they would from
// compiled Go through the guard.
func TestExprPanics(t *testing.T) {
	rt := exprRuntime(t)
	for _, tc := range []struct{ src, want string }{
		{`a := 1; b := 0; return a / b;`, "divide by zero"},
		{`a := 1; b := 0; return a % b;`, "divide by zero"},
		{`xs := seq(); i := 9; return xs[i];`, "out of range"},
		{`n := 1; s := -1; return n << s;`, "negative shift"},
	} {
		_, err := rt.Eval[any](tc.src, nil)
		var pe *PanicError
		if !errors.As(err, &pe) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want panic containing %q", tc.src, err, tc.want)
		}
	}
}

// TestExprCompileErrors pins the typing rules: identical operand
// types, no stack operands, foldable failures at compile time.
func TestExprCompileErrors(t *testing.T) {
	rt := exprRuntime(t)
	for name, tc := range map[string]struct{ src, want string }{
		"mismatched":   {`a := i32(1); b := 2; c := a + b0(); return c;`, ""},
		"mixed types":  {`a := i32(1); s := "x"; c := a + s; return c;`, "mismatched types"},
		"stack name":   {`c := v + 1; return c;`, "cannot be an operand"},
		"div by zero":  {`c := 1 / 0; return c;`, "division by zero"},
		"float mod":    {`c := 1.5 % 2.0; return c;`, "invalid operation"},
		"not bool":     {`a := 1; b := 2; c := a && b; return c;`, "wants bool operands"},
		"not nilable":  {`a := 1; c := a == nil; return c;`, "not nilable"},
		"not index":    {`a := 1; c := a[0]; return c;`, "not indexable"},
		"len of int":   {`a := 1; c := len(a); return c;`, "len of"},
		"repr":         {`n := i8(1); n = n + 300; return n;`, "overflows"},
	} {
		if tc.want == "" {
			continue
		}
		_, err := rt.Compile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

// TestExprNilCompare covers the nil comparisons over pointers and
// slices; errors as comparable values arrive with the hybrid error
// binding.
func TestExprNilCompare(t *testing.T) {
	rt := exprRuntime(t)
	got, err := rt.Eval[bool](`p := pnil(); q := pval(); xs := seq(); return p == nil && q != nil && xs != nil;`, nil)
	if err != nil || !got {
		t.Fatal(got, err)
	}
}
