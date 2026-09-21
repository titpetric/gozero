package gozero

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// funclitRuntime binds the surface the func literal tests share. Tests
// add their own capture bindings on top with rt.Bind.
func funclitRuntime(tb testing.TB) *Runtime {
	tb.Helper()
	rt := NewRuntime()
	for scope, fns := range map[string]map[string]any{
		"http": {"NewServeMux": http.NewServeMux},
		"httptest": {
			"NewRequest":  httptest.NewRequest,
			"NewRecorder": httptest.NewRecorder,
		},
		"fmt": {"Fprint": fmt.Fprint, "Sprint": fmt.Sprint, "Sprintf": fmt.Sprintf},
		"assert": {
			"Equal": assertEqual,
			"True":  assertTrue,
		},
	} {
		if err := rt.BindScope(scope, fns); err != nil {
			tb.Fatal(err)
		}
	}
	return rt
}

// funclitPair compiles src down both tiers. Unlike compilePair it
// tolerates bridged calls: a body statement may bridge (an interface
// method does) while the program still runs on the direct tier.
func funclitPair(t *testing.T, rt *Runtime, src string) (jit, slow CompiledFunc) {
	t.Helper()
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p, err := rt.compiler.compileProgram(prog)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	jp, err := jitCompileProgram(p)
	if err != nil {
		t.Fatalf("program did not JIT: %s: %v", src, err)
	}
	return jp.run, p.run
}

// TestFuncLitRules pins every rule that rejects a literal at compile
// time, by the message that names it.
func TestFuncLitRules(t *testing.T) {
	rt := funclitRuntime(t)
	if err := rt.Bind("needsResult", func(f func() string) string { return f() }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("variadicFn", func(f func(...string)) { f("x") }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("twoResults", func(f func() (string, string)) { f() }); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		// The expected type is not a func signature.
		`fmt.Sprint(func() { });`: "a func literal fills only a func-typed parameter",
		`h := func() { };`:        "cannot be assigned to a name",
		`return func() { };`:      "a func literal fills only a func-typed parameter",
		// The parameter list against the signature.
		`mux := http.NewServeMux(); mux.HandleFunc("/", func(w) { });`:       "names 1 parameters",
		`mux := http.NewServeMux(); mux.HandleFunc("/", func(w, r, x) { });`: "names 3 parameters",
		`mux := http.NewServeMux(); mux.HandleFunc("/", func(w, w) { });`:    "parameter \"w\" repeats",
		`mux := http.NewServeMux(); mux.HandleFunc("/", func(fmt, r) { });`:  "shadows a binding or keyword",
		`mux := http.NewServeMux(); mux.HandleFunc("/", func(dest, r) { });`: "shadows a binding or keyword",
		`variadicFn(func(xs) { });`:                                          "is variadic",
		`twoResults(func() { });`:                                            "more than one result besides error",
		// Capture is a compile error, named.
		`s := fmt.Sprint("x"); mux := http.NewServeMux(); mux.HandleFunc("/", func(w, r) { fmt.Fprint(w, s) });`: "s is a name of the enclosing program",
		`mux := http.NewServeMux(); mux.HandleFunc("/", func(w, r) { fmt.Fprint(w, tb) });`:                      "tb is neither a parameter nor a name the body defines",
		`mux := http.NewServeMux(); mux.HandleFunc("/", func(w, r) { mux.ServeHTTP(w, r) });`:                    "mux is a name of the enclosing program",
		// Parameters are names of the body's scope, not redeclarable.
		`mux := http.NewServeMux(); mux.HandleFunc("/", func(w, r) { var w chan string; });`: "var w redeclares a parameter",
		// The body's returns against the signature.
		`mux := http.NewServeMux(); mux.HandleFunc("/", func(w, r) { return "x" });`: "the body returns a value",
		`needsResult(func() { fmt.Sprint("x") });`:                                   "returns a value and the body never does",
	}
	for src, want := range cases {
		_, err := rt.Compile(src)
		if err == nil {
			t.Errorf("%s: compiled, want error containing %q", src, want)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s:\n  got  %v\n  want %q", src, err, want)
		}
	}
}

// TestFuncLitBothTiers runs the handler program down both tiers and
// asserts the same observable effects: status and body written by the
// literal through the recorder, including the bridged interface
// method call inside the body.
func TestFuncLitBothTiers(t *testing.T) {
	rt := funclitRuntime(t)
	const src = `
		mux := http.NewServeMux();
		mux.HandleFunc("/health", func(w, r) {
			w.WriteHeader(201);
			fmt.Fprint(w, "ok");
		});
		req := httptest.NewRequest("GET", "/health");
		rec := httptest.NewRecorder();
		mux.ServeHTTP(rec, req);
		out := fmt.Sprintf("%d %s", rec.Code, rec.Body.String());
		return out;
	`
	jit, slow := funclitPair(t, rt, src)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		out, err := fn(t.Context(), nil, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out != "201 ok" {
			t.Fatalf("%s: got %v, want 201 ok", name, out)
		}
	}
}

// TestFuncLitResultSignatures covers the signatures that stay on the
// MakeFunc bridge: a value result, and a trailing error result, on
// both tiers.
func TestFuncLitResultSignatures(t *testing.T) {
	rt := funclitRuntime(t)
	if err := rt.Bind("wrap", func(f func() string) string { return f() }); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := rt.Bind("fail", func() error { return boom }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("callErr", func(f func() error) error { return f() }); err != nil {
		t.Fatal(err)
	}

	src := `out := wrap(func() { return "hi" }); return out;`
	jit, slow := funclitPair(t, rt, src)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		out, err := fn(t.Context(), nil, nil)
		if err != nil || out != "hi" {
			t.Fatalf("%s: got %v, %v", name, out, err)
		}
	}
	// Supports names the literal as bridged: a result has no slot in
	// the closure table.
	if err := rt.Supports(src); err == nil || !strings.Contains(err.Error(), "func literal") {
		t.Fatalf("Supports should name the bridged literal, got %v", err)
	}

	errSrc := `callErr(func() { fail() });`
	jit, slow = funclitPair(t, rt, errSrc)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		_, err := fn(t.Context(), nil, nil)
		if !errors.Is(err, boom) {
			t.Fatalf("%s: got %v, want boom", name, err)
		}
	}
}

// TestFuncLitPanics pins the no-error-result contract on both tiers: a
// failing body panics with its error when the signature gives the
// error no result to travel in, matching FuncOf.
func TestFuncLitPanics(t *testing.T) {
	rt := funclitRuntime(t)
	boom := errors.New("boom")
	if err := rt.Bind("fail", func() error { return boom }); err != nil {
		t.Fatal(err)
	}
	var run func()
	if err := rt.Bind("keep", func(f func()) { run = f }); err != nil {
		t.Fatal(err)
	}
	jit, slow := funclitPair(t, rt, `keep(func() { fail() });`)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		run = nil
		if _, err := fn(t.Context(), nil, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("%s: no panic", name)
				}
			}()
			run()
		}()
	}
}

// TestFuncLitNested nests a literal in a literal's body. The inner
// literal's enclosing scope is the outer body: its own parameters
// stay readable through nesting, and reading across a literal
// boundary stays a named error.
func TestFuncLitNested(t *testing.T) {
	rt := funclitRuntime(t)
	count := 0
	if err := rt.Bind("twice", func(f func()) { f(); f() }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("touch", func() { count++ }); err != nil {
		t.Fatal(err)
	}
	jit, slow := funclitPair(t, rt, `twice(func() { twice(func() { touch() }) });`)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		count = 0
		if _, err := fn(t.Context(), nil, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if count != 4 {
			t.Fatalf("%s: touched %d times, want 4", name, count)
		}
	}

	// The outer literal's parameter is an enclosing name to the inner
	// literal: capture across literal boundaries is the same rule.
	if err := rt.Bind("give", func(f func(string)) { f("x") }); err != nil {
		t.Fatal(err)
	}
	src := `give(func(s) { twice(func() { fmt.Sprint(s) }) });`
	_, err := rt.Compile(src)
	if err == nil || !strings.Contains(err.Error(), "s is a name of the enclosing program") {
		t.Fatalf("got %v, want the capture rule naming s", err)
	}
}

// TestFuncLitParamShadowing allows a parameter to reuse a name the
// enclosing program also binds: scopes do not nest, so there is
// nothing to shadow, and each side reads its own.
func TestFuncLitParamShadowing(t *testing.T) {
	rt := funclitRuntime(t)
	var run func(string)
	if err := rt.Bind("keep", func(f func(string)) { run = f }); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := rt.Bind("sink", func(s string) { got = s }); err != nil {
		t.Fatal(err)
	}
	jit, slow := funclitPair(t, rt, `s := fmt.Sprint("outer"); keep(func(s) { sink(s) }); sink(s);`)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		got = ""
		if _, err := fn(t.Context(), nil, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != "outer" {
			t.Fatalf("%s: after the run got %q", name, got)
		}
		run("inner")
		if got != "inner" {
			t.Fatalf("%s: after the call got %q", name, got)
		}
	}
}

// TestFuncLitBodyBlocks runs a body holding the statement kinds a
// block admits: an if chain with an early return, and a loop. The two
// tiers agree on the effects, and the direct tier consumes the return
// signal a bare return raises instead of panicking with it.
func TestFuncLitBodyBlocks(t *testing.T) {
	rt := funclitRuntime(t)
	var handler http.HandlerFunc
	if err := rt.Bind("keep", func(h http.HandlerFunc) { handler = h }); err != nil {
		t.Fatal(err)
	}
	const src = `keep(func(w, r) {
		if r.URL.Path == "/skip" {
			return;
		}
		for i := 0; i < 3; i++ {
			fmt.Fprint(w, "ok");
		}
	});`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("a block body should reach the closure table: %v", err)
	}
	jit, slow := funclitPair(t, rt, src)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		handler = nil
		if _, err := fn(t.Context(), nil, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest("GET", "/serve", nil))
		if got := rec.Body.String(); got != "okokok" {
			t.Fatalf("%s: got %q, want okokok", name, got)
		}
		rec = httptest.NewRecorder()
		handler(rec, httptest.NewRequest("GET", "/skip", nil))
		if got := rec.Body.String(); got != "" {
			t.Fatalf("%s: the early return wrote %q", name, got)
		}
	}
}

// TestFuncLitBlockCapture pins the capture rule inside a nested
// statement list: an arm and a loop body are the literal's own scope,
// so a name read there falls under the same rule a read at the top of
// the body does.
func TestFuncLitBlockCapture(t *testing.T) {
	rt := funclitRuntime(t)
	if err := rt.Bind("keep", func(http.HandlerFunc) {}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		`s := fmt.Sprint("x"); keep(func(w, r) { if r.URL.Path == "/" { fmt.Fprint(w, s) } });`:  "s is a name of the enclosing program",
		`keep(func(w, r) { if r.URL.Path == "/" { fmt.Fprint(w, tb) } });`:                       "tb is neither a parameter nor a name the body defines",
		`s := fmt.Sprint("x"); keep(func(w, r) { for i := 0; i < 2; i++ { fmt.Fprint(w, s) } });`: "s is a name of the enclosing program",
		`s := fmt.Sprint("x"); keep(func(w, r) { if s == "x" { fmt.Fprint(w, "y") } });`:          "s is a name of the enclosing program",
	}
	for src, want := range cases {
		_, err := rt.Compile(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s:\n  got  %v\n  want %q", src, err, want)
		}
	}
}
