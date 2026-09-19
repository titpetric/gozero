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
// hold and what it cannot iterate. return and var are rejected where
// they are written, break and continue outside a body and labels on
// either are rejected by name, the ranged expression needs a static
// type, and the channel and iterator forms bound their variable
// counts.
func TestRangeCompileErrors(t *testing.T) {
	rt, _ := rangeRuntime(t)
	for name, fn := range map[string]any{
		"sendonly": func() chan<- string { return nil },
		"mkch":     func() chan string { return nil },
		"notseq":   func() func(int) int { return nil },
		"lines":    func() func(func(string) bool) { return nil },
		"parse":    url.Parse,
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct{ src, want string }{
		"break outside":     {`break`, "break is only allowed inside a range body"},
		"continue outside":  {`continue`, "continue is only allowed inside a range body"},
		"break label":       {`for i := range 3 { break out }`, "a label after break is not in the language"},
		"continue label":    {`for i := range 3 { continue out }`, "a label after continue is not in the language"},
		"return in body":    {`for i := range 3 { return i }`, "return cannot stand inside a range body"},
		"var in body":       {`for i := range 3 { var u url.URL }`, "var declaration cannot stand inside a range body"},
		"assign form":       {`i := 0; for i = range 3 { poke() }`, "declares its names with :="},
		// The two-name cap is Go's own grammar rule, so the message is
		// go/parser's rather than a house one.
		"three names":       {`for a, b, c := range 3 { poke() }`, "expected at most 2 expressions"},
		"no range":          {`for poke() { }`, "only the range form"},
		"two int vars":      {`for i, v := range 3 { touch(i) }`, "permits one iteration variable"},
		"two chan vars":     {`c := mkch(); for v, ok := range c { rec(v) }`, "permits one iteration variable"},
		"two seq vars":      {`for a, b := range lines() { rec(a) }`, "permits one iteration variable"},
		"send-only channel": {`c := sendonly(); for v := range c { rec(v) }`, "cannot range over the send-only"},
		"not an iterator":   {`for v := range notseq() { idx(v) }`, "a range func is func(func(V) bool) or func(func(K, V) bool)"},
		"struct bound":      {`u := parse("https://h/p"); for v := range u { poke() }`, "cannot range over *url.URL"},
		"stack name":        {`for _, s := range xs { rec(s) }`, "not a name bound by the program"},
		"unterminated":      {`for i := range 3 { poke();`, "unterminated range body"},
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
