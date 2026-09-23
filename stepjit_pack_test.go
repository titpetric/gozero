package gozero

import (
	"strings"
	"testing"
)

// TestPackShapes covers the variadic pack on the direct tier: the two
// element types the table carries, an empty tail, and an element type
// outside it, which bridges rather than failing.
func TestPackShapes(t *testing.T) {
	rt, seen := bvRuntime(t)
	for name, v := range map[string]any{
		"sprintf": func(format string, a ...any) string { return "" },
		"joinAny": func(a ...any) (string, error) {
			parts := make([]string, len(a))
			for i, v := range a {
				parts[i], _ = v.(string)
			}
			return strings.Join(parts, ","), nil
		},
		"joinStr": func(a ...string) (string, error) { return strings.Join(a, ","), nil },
		"sumInts": func(a ...int) (int, error) {
			n := 0
			for _, x := range a {
				n += x
			}
			return n, nil
		},
	} {
		if err := rt.Bind(name, v); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct{ src, want string }{
		{"s := joinAny(\"a\", \"b\")\nrecordS(s)", "a,b"},
		{"s := joinStr(\"a\", \"b\")\nrecordS(s)", "a,b"},
		{"s := joinAny()\nrecordS(s)", ""},
		{"s := joinStr()\nrecordS(s)", ""},
	} {
		if err := bvRun(t, rt, tc.src); err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if seen.s != tc.want {
			t.Errorf("%s: got %q, want %q", tc.src, seen.s, tc.want)
		}
	}

	// A spread passes the slice the program already holds.
	if err := bvRun(t, rt, "xs := []string{\"a\", \"b\"}\ns := joinStr(xs...)\nrecordS(s)"); err != nil {
		t.Fatal(err)
	}
	if seen.s != "a,b" {
		t.Errorf("spread: got %q", seen.s)
	}

	// An element type outside the table bridges; the program still
	// runs and Supports names the call.
	if err := bvRun(t, rt, "n := sumInts(1, 2, 3)\nrecordN(n)"); err != nil {
		t.Fatal(err)
	}
	if seen.n != 6 {
		t.Errorf("sumInts = %d, want 6", seen.n)
	}
	if err := rt.Supports("n := sumInts(1, 2, 3)\nrecordN(n)"); err == nil ||
		!strings.Contains(err.Error(), "packing []int is not in the table") {
		t.Errorf("want the pack to name its gap, got %v", err)
	}
}
