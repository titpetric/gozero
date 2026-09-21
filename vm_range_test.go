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
		// isA is the predicate the if-inside-a-loop cases branch on.
		"isA": func(s string) bool { return s == "a" },
		"yes": func() bool { return true },
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
// hold and what it cannot iterate. return and var are rejected where
// they are written, break and continue outside a body and labels on
// either are rejected by name, the ranged expression needs a static
// type, and only slices, arrays and integers iterate.
func TestRangeCompileErrors(t *testing.T) {
	rt, _ := rangeRuntime(t)
	if err := rt.Bind("hdr", func() map[string][]string { return nil }); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ src, want string }{
		"break outside":    {`poke(); break`, "break is only allowed inside a loop body"},
		"continue outside": {`continue; poke()`, "continue is only allowed inside a loop body"},
		"break after":      {`for i := range 2 { touch(i) }; break`, "break is only allowed inside a loop body"},
		"break label":      {`for i := range 3 { break out }`, "a label after break is not in the language"},
		"continue label":   {`for i := range 3 { continue out }`, "a label after continue is not in the language"},
		"return in body":   {`for i := range 3 { return i }`, "return cannot stand inside a loop body"},
		"var in body":      {`for i := range 3 { var u url.URL }`, "var declaration cannot stand inside a loop body"},
		"assign form":      {`i := 0; for i = range 3 { poke() }`, "declares its names with :="},
		"three names":      {`for a, b, c := range 3 { poke() }`, "at most two names"},
		"named no range":   {`for i, j := 0; i < 3; i++ { poke() }`, "declares exactly one name"},
		"map":              {`m := hdr(); for k := range m { rec(k) }`, "cannot range over map[string][]string"},
		"string":           {`s := "abc"; for i := range s { touch(i) }`, "cannot range over string"},
		"two int vars":     {`for i, v := range 3 { touch(i) }`, "permits one iteration variable"},
		"stack name":       {`for _, s := range xs { rec(s) }`, "not a name bound by the program"},
		"unterminated":     {`for i := range 3 { poke();`, "unterminated block"},
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

// TestRangeReflectTier runs a range form the direct tier declines, so
// the reflect evaluator's own loop is what executes: an array bound,
// which has no slice header.
func TestRangeReflectTier(t *testing.T) {
	rt, seen := rangeRuntime(t)
	if err := rt.Bind("arr", func() [3]string { return [3]string{"x", "y", "z"} }); err != nil {
		t.Fatal(err)
	}
	const src = `
		a := arr();
		for i, s := range a {
			idx(i);
			rec(s);
		}
	`
	if err := rt.Supports(src); err == nil {
		t.Error("an array bound should decline the direct tier")
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	*seen = nil
	if _, err := fn(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprintf("%v", *seen), "[idx:0 x idx:1 y idx:2 z]"; got != want {
		t.Errorf("effects %s, want %s", got, want)
	}
}
