package gozero

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"testing"
)

// rangeRuntime binds the surface the range tests drive. Every binding
// fits the shape table, so the programs are direct programs and the
// equivalence tests compare the two tiers rather than reflect twice.
func rangeRuntime(t testing.TB) (*Runtime, *[]string) {
	t.Helper()
	rt := NewRuntime()
	var seen []string
	bind := map[string]any{
		"fields": strings.Fields,
		"join":   path.Join,
		// rec records a string; the pointer result gives it a shape.
		"rec": func(s string) *url.URL {
			seen = append(seen, s)
			return nil
		},
		// touch records an int64, idx an int: the two key types.
		"touch": func(n int64) error {
			seen = append(seen, fmt.Sprintf("touch:%d", n))
			return nil
		},
		"idx": func(n int) error {
			seen = append(seen, fmt.Sprintf("idx:%d", n))
			return nil
		},
		"poke": func() *url.URL {
			seen = append(seen, "poke")
			return nil
		},
		"count": func() int { return 3 },
		"zero":  func() int { return 0 },
		"neg":   func() int { return -2 },
		"fail": func(s string) (*url.URL, error) {
			return nil, fmt.Errorf("boom: %s", s)
		},
	}
	for name, fn := range bind {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	return rt, &seen
}

// TestRangeCompileErrors pins the named rules: what a range cannot
// hold and what it cannot iterate. break, continue, return and var
// are rejected where they are written, the ranged expression needs a
// static type, and only slices, arrays and integers iterate.
func TestRangeCompileErrors(t *testing.T) {
	rt, _ := rangeRuntime(t)
	if err := rt.Bind("hdr", func() map[string][]string { return nil }); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ src, want string }{
		"break":          {`for i := range 3 { break }`, "break is not in the language"},
		"continue":       {`for i := range 3 { continue }`, "continue is not in the language"},
		"return in body": {`for i := range 3 { return i }`, "return cannot stand inside a range body"},
		"var in body":    {`for i := range 3 { var u url.URL }`, "var declaration cannot stand inside a range body"},
		"assign form":    {`i := 0; for i = range 3 { poke() }`, "declares its names with :="},
		"three names":    {`for a, b, c := range 3 { poke() }`, "at most two names"},
		"no range":       {`for poke() { }`, "only the range form"},
		"map":            {`m := hdr(); for k := range m { rec(k) }`, "cannot range over map[string][]string"},
		"string":         {`s := "abc"; for i := range s { touch(i) }`, "cannot range over string"},
		"two int vars":   {`for i, v := range 3 { touch(i) }`, "permits one iteration variable"},
		"stack name":     {`for _, s := range xs { rec(s) }`, "not a name bound by the program"},
		"unterminated":   {`for i := range 3 { poke();`, "unterminated range body"},
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

// TestRangeReflectTier runs range forms the direct tier declines, so
// the reflect evaluator's own loop is what executes: an array bound
// and a struct element type, neither of which has a slice header or
// a layout class.
func TestRangeReflectTier(t *testing.T) {
	rt, seen := rangeRuntime(t)
	if err := rt.Bind("arr", func() [3]string { return [3]string{"x", "y", "z"} }); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		src  string
		want string
	}{
		"array": {
			src: `
				a := arr();
				for i, s := range a {
					idx(i);
					rec(s);
				}
			`,
			want: "[idx:0 x idx:1 y idx:2 z]",
		},
	} {
		if err := rt.Supports(tc.src); err == nil {
			t.Errorf("%s: expected the direct tier to decline", name)
		}
		fn, err := rt.Compile(tc.src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		*seen = nil
		if _, err := fn(context.Background(), nil, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := "[" + strings.Join(*seen, " ") + "]"; got != tc.want {
			t.Errorf("%s: effects %s, want %s", name, got, tc.want)
		}
	}
}
