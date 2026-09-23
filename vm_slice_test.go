package gozero

import (
	"strings"
	"testing"
)

// TestSliceLiteral covers []T{...} in the three places a value can
// stand: an argument, a name, and the right of an assignment.
func TestSliceLiteral(t *testing.T) {
	rt, seen := bvRuntime(t)
	if err := rt.Bind("joinAll", func(sep string, v []string) (string, error) {
		return strings.Join(v, sep), nil
	}); err != nil {
		t.Fatal(err)
	}

	// An argument.
	if err := bvRun(t, rt, `recordL([]string{"a", "b"})`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen.l, ",") != "a,b" {
		t.Errorf("argument literal read as %v", seen.l)
	}
	// A name, and a trailing comma as in Go.
	if err := bvRun(t, rt, "xs := []string{\"a\", \"b\",}\nrecordL(xs)"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen.l, ",") != "a,b" {
		t.Errorf("named literal read as %v", seen.l)
	}
	// Empty.
	if err := bvRun(t, rt, `recordL([]string{})`); err != nil {
		t.Fatal(err)
	}
	if len(seen.l) != 0 {
		t.Errorf("empty literal read as %v", seen.l)
	}
	// Elements can be calls, and the value spreads into a variadic.
	if err := bvRun(t, rt, "s := joinAll(\"/\", []string{\"a\", \"b\"})\nrecordS(s)"); err != nil {
		t.Fatal(err)
	}
	if seen.s != "a/b" {
		t.Errorf("joinAll = %q", seen.s)
	}
	// A scalar element type.
	if err := rt.Bind("sum", func(v []int) (int, error) {
		n := 0
		for _, x := range v {
			n += x
		}
		return n, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bvRun(t, rt, `recordN(sum([]int{1, 2, 3}))`); err != nil {
		t.Fatal(err)
	}
	if seen.n != 6 {
		t.Errorf("sum = %d, want 6", seen.n)
	}
}

// TestSliceLiteralIsFreshPerRun pins that the literal allocates on
// every evaluation, the way a Go composite literal does: a binding
// that wrote into one run's slice must not be seen by the next.
func TestSliceLiteralIsFreshPerRun(t *testing.T) {
	rt, seen := bvRuntime(t)
	fn, err := rt.Compile("xs := []string{\"a\"}\npokeL(xs)\nrecordL(xs)")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := fn.Exec[any](nil); err != nil {
			t.Fatal(err)
		}
		if strings.Join(seen.l, ",") != "poked" {
			t.Fatalf("run %d read %v", i, seen.l)
		}
	}
}

// TestSliceLiteralRejections pins the compile-time checks, which are
// the binding's own and the registry's.
func TestSliceLiteralRejections(t *testing.T) {
	rt, _ := bvRuntime(t)
	for _, tc := range []struct{ src, want string }{
		{`recordL([]nosuch{"a"})`, "unknown type"},
		{`recordL([]string{1})`, "cannot use"},
		{`recordN([]int{1})`, "unknown type"},
		{`recordL([]string{Key: "a"})`, "expected ',' or '}'"},
	} {
		err := bvRun(t, rt, tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want %q, got %v", tc.src, tc.want, err)
		}
	}
}
