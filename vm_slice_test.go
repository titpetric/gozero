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
	// A literal beside another argument. The type the literal names
	// has to fit the parameter at position 2, the one it fills.
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
// every evaluation, the way a Go composite literal does: the next run
// must not read what a binding wrote into the previous run's slice.
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

// TestSliceLiteralElementCall covers a call as an element of a
// literal, and a literal spread into a variadic parameter. A comment
// here claimed both for a case that pins neither: that binding takes a
// slice, and its two elements are constants.
//
// The element call matters because a literal builds per evaluation.
// The call standing in the literal runs on the same schedule as the
// build. The spread matters because the literal and the variadic pack
// are two allocations the direct tier treats separately.
func TestSliceLiteralElementCall(t *testing.T) {
	rt, seen := bvRuntime(t)
	if err := rt.Bind("joinVar", func(sep string, parts ...string) (string, error) {
		return strings.Join(parts, sep), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("upper", strings.ToUpper); err != nil {
		t.Fatal(err)
	}

	// An element that is a call. first() reads the bound slice, which
	// puts the element's value behind a call the build has to run.
	if err := rt.Bind("names", []string{"ada", "grace"}); err != nil {
		t.Fatal(err)
	}
	if err := bvRun(t, rt, `recordL([]string{upper(first(names))})`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen.l, ",") != "ADA" {
		t.Errorf("an element call read %v, want [ADA]", seen.l)
	}

	// A literal spread into a variadic parameter.
	if err := bvRun(t, rt, "s := joinVar(\"/\", []string{\"a\", \"b\"}...)\nrecordS(s)"); err != nil {
		t.Fatal(err)
	}
	if seen.s != "a/b" {
		t.Errorf("a spread literal read %q, want a/b", seen.s)
	}
}
