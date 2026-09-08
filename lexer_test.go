package gozero

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func litRuntime(t *testing.T) (*Runtime, *any) {
	t.Helper()
	rt := NewRuntime()
	var seen any
	for name, fns := range map[string]map[string]any{
		"http": {"NewRequest": http.NewRequest},
		"json": {"NewEncoder": json.NewEncoder},
		"url":  {"Parse": url.Parse},
	} {
		if err := rt.BindScope(name, fns); err != nil {
			t.Fatal(err)
		}
	}
	for name, fn := range map[string]any{
		"wantBool": func(b bool) (*url.URL, error) { seen = b; return &url.URL{}, nil },
		"wantI64":  func(n int64) (*url.URL, error) { seen = n; return &url.URL{}, nil },
		"wantAny":  func(v any) (*url.URL, error) { seen = v; return &url.URL{}, nil },
		"mixed":    func(s string, n int64) (*url.URL, error) { seen = n; return &url.URL{Path: s}, nil },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	return rt, &seen
}

// TestBoolLiteral pins the fix for the worst bug the review found:
// wantBool(true) compiled, ran, and handed the callee false, because
// true parsed as a variable reference, missed the stack, and
// zero-filled. true, false and nil are keywords now.
func TestBoolLiteral(t *testing.T) {
	rt, seen := litRuntime(t)
	for _, tc := range []struct {
		src  string
		want any
	}{
		{`wantBool(true);`, true},
		{`wantBool(false);`, false},
		{`wantAny(true);`, true},
		{`var b bool; b = true; wantBool(b);`, true},
		{`b = true; wantBool(b);`, true},
		{`var b bool; wantBool(b);`, false},
	} {
		*seen = "sentinel"
		fn, err := rt.Compile(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if _, err := fn.Exec[any](nil); err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if *seen != tc.want {
			t.Errorf("%s: callee saw %v (%T), want %v", tc.src, *seen, *seen, tc.want)
		}
	}

	// A bool literal in a slot of the wrong type is a compile error.
	if _, err := rt.Compile(`wantI64(true);`); err == nil {
		t.Error("expected true not to fill an int64 parameter")
	}
}

// TestNilLiteral checks nil against nilable and non-nilable parameters.
func TestNilLiteral(t *testing.T) {
	rt, seen := litRuntime(t)
	*seen = "sentinel"
	fn, err := rt.Compile(`return http.NewRequest("GET", "/x", nil);`)
	if err != nil {
		t.Fatal(err)
	}
	req, err := fn.Exec[*http.Request](nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Body != nil {
		t.Errorf("Body = %v, want nil from the literal", req.Body)
	}

	if _, err := rt.Compile(`wantI64(nil);`); err == nil {
		t.Error("expected nil not to fill an int64 parameter")
	}
	if _, err := rt.Compile(`wantBool(nil);`); err == nil {
		t.Error("expected nil not to fill a bool parameter")
	}
	if _, err := rt.Compile(`x = nil;`); err == nil {
		t.Error("expected x = nil to need a type")
	}
	if _, err := rt.Compile(`var r *http.Request; r = nil; wantAny("done");`); err != nil {
		t.Errorf("nil into a declared pointer: %v", err)
	}
}

// TestReturnValue covers the return forms that are not calls: a name, a
// literal, a field.
func TestReturnValue(t *testing.T) {
	rt, _ := litRuntime(t)
	for _, tc := range []struct {
		name, src string
		check     func(t *testing.T, fn CompiledFunc)
	}{
		{"a bound name", `u := url.Parse("/a"); return u;`, func(t *testing.T, fn CompiledFunc) {
			u, err := fn.Exec[*url.URL](nil)
			if err != nil || u.Path != "/a" {
				t.Errorf("got %v, %v", u, err)
			}
		}},
		{"a scalar name", `var x int64; x = 42; return x;`, func(t *testing.T, fn CompiledFunc) {
			n, err := fn.Exec[int64](nil)
			if err != nil || n != 42 {
				t.Errorf("got %v, %v", n, err)
			}
		}},
		{"a literal", `return 7;`, func(t *testing.T, fn CompiledFunc) {
			n, err := fn.Exec[int64](nil)
			if err != nil || n != 7 {
				t.Errorf("got %v, %v", n, err)
			}
		}},
		{"a bool literal", `return true;`, func(t *testing.T, fn CompiledFunc) {
			b, err := fn.Exec[bool](nil)
			if err != nil || !b {
				t.Errorf("got %v, %v", b, err)
			}
		}},
		{"a field", `req := http.NewRequest("GET", "/f"); return req.URL;`, func(t *testing.T, fn CompiledFunc) {
			u, err := fn.Exec[*url.URL](nil)
			if err != nil || u.Path != "/f" {
				t.Errorf("got %v, %v", u, err)
			}
		}},
		{"a stack name", `return v;`, func(t *testing.T, fn CompiledFunc) {
			got, err := fn.Exec[string](map[string]any{"v": "from stack"})
			if err != nil || got != "from stack" {
				t.Errorf("got %v, %v", got, err)
			}
		}},
	} {
		fn, err := rt.Compile(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		tc.check(t, fn)
	}

	if _, err := rt.Compile(`return nil;`); err == nil {
		t.Error("return nil should say to use return;")
	}
}

// TestReturnNameJITs checks that returning a program-bound name reaches
// the direct-call tier and produces the same value as reflect. An error
// mid-program must still win over the return.
func TestReturnNameJITs(t *testing.T) {
	rt, _ := litRuntime(t)
	const src = `u := url.Parse("/j"); return u;`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("did not JIT: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	u, err := fn.Exec[*url.URL](nil)
	if err != nil || u.Path != "/j" {
		t.Fatalf("got %v, %v", u, err)
	}

	const failing = `u := url.Parse(bad); return u;`
	fn, err = rt.Compile(failing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[*url.URL](map[string]any{"bad": 42}); err == nil {
		t.Error("expected the type error to beat the return")
	}
}

// TestAssignNameToName pins the error message for x = y, which used to
// surface as a bare "expected '('".
func TestAssignNameToName(t *testing.T) {
	rt, _ := litRuntime(t)
	_, err := rt.Compile(`x = 5; y = x;`)
	if err == nil || !strings.Contains(err.Error(), "cannot assign a name to a name") {
		t.Errorf("err = %v", err)
	}
}

// TestParseAllocBudget stops the parse and compile cost creeping
// silently: it moved 1088 to 1256 to 1416 B/op across three grammar
// expansions with nothing watching. Measured 29 allocations; the
// ceiling is that plus headroom for one small feature, not a target.
func TestParseAllocBudget(t *testing.T) {
	rt, _ := litRuntime(t)
	const src = `return http.NewRequest("GET", url);`
	n := testing.AllocsPerRun(200, func() {
		if _, err := rt.compileUncached(src); err != nil {
			panic(err)
		}
	})
	t.Logf("parse+compile allocations/run = %.0f", n)
	if n > 32 {
		t.Errorf("parse+compile allocates %.0f/run, budget 32", n)
	}
}

// TestStringEscapes pins escape interpretation. Before this, the byte
// after a backslash was appended verbatim, so "a\nb" silently became
// "anb": the named escapes are interpreted, and an unknown one is a
// parse error rather than a quiet mangle.
func TestStringEscapes(t *testing.T) {
	rt, seen := litRuntime(t)
	for _, tc := range []struct{ src, want string }{
		{`wantAny("a\"b");`, `a"b`},
		{`wantAny("a\nb");`, "a\nb"},
		{`wantAny("a\tb");`, "a\tb"},
		{`wantAny("a\rb");`, "a\rb"},
		{`wantAny("a\\b");`, `a\b`},
		{`wantAny('a\'b');`, "a'b"},
	} {
		*seen = nil
		fn, err := rt.Compile(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if _, err := fn.Exec[any](nil); err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if *seen != tc.want {
			t.Errorf("%s: callee saw %q, want %q", tc.src, *seen, tc.want)
		}
	}
	if _, err := rt.Compile(`wantAny("a\qb");`); err == nil {
		t.Error("an unknown escape must be a parse error, not a silent mangle")
	}
}

// TestStatementsAcrossLines pins the line rules: the end of a line
// closes a statement the way a semicolon does, a semicolon separates
// statements sharing a line, and a statement still spans lines freely
// inside its parentheses. The end of a line is a terminator, so a
// return's value has to start on the return's own line.
func TestStatementsAcrossLines(t *testing.T) {
	rt, _ := litRuntime(t)
	for _, src := range []string{
		`u := url.Parse("/nl"); return u;`,
		"u := url.Parse(\"/nl\")\nreturn u",
		"u := url.Parse(\"/nl\"); return u",
		"u := url.Parse(\n\t\"/nl\"\n)\nreturn u",
	} {
		fn, err := rt.Compile(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		u, err := fn.Exec[*url.URL](nil)
		if err != nil || u.Path != "/nl" {
			t.Errorf("%q: got %v, %v", src, u, err)
		}
	}
	// A return followed by a newline is a bare return; the name on the
	// next line is a statement of its own, which a name cannot be.
	if _, err := rt.Compile("u := url.Parse(\"/nl\")\nreturn\n\tu\n"); err == nil {
		t.Error("expected the stranded name after a bare return to fail")
	}
}
