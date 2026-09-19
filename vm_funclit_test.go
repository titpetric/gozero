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
		// A name that is neither local nor of the enclosing program is
		// an error: stack names do not cross the literal boundary.
		`mux := http.NewServeMux(); mux.HandleFunc("/", func(w, r) { fmt.Fprint(w, tb) });`: "tb is neither a parameter, a name the body defines, nor a name of the enclosing program",
		// A declaration of a name the body already captured is one name
		// for two variables.
		`s := fmt.Sprint("x"); mux := http.NewServeMux(); mux.HandleFunc("/", func(w, r) { fmt.Fprint(w, s); s := fmt.Sprint("y"); fmt.Fprint(w, s) });`:   "s is declared after the body captured it",
		`s := fmt.Sprint("x"); mux := http.NewServeMux(); mux.HandleFunc("/", func(w, r) { fmt.Fprint(w, s); var s chan string; fmt.Fprint(w, s) });`:      "s is declared after the body captured it",
		// A captured cell holds one type for its whole life.
		`s := fmt.Sprint("x"); mux := http.NewServeMux(); mux.HandleFunc("/", func(w, r) { fmt.Fprint(w, s); s = httptest.NewRecorder() });`:               "captured s is reassigned from string to *httptest.ResponseRecorder",
		`s := fmt.Sprint("x"); mux := http.NewServeMux(); mux.HandleFunc("/", func(w, r) { fmt.Fprint(w, s) }); s = httptest.NewRecorder(); fmt.Sprint(s)`: "a captured name is reassigned from string to *httptest.ResponseRecorder",
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
	// literal: the inner body captures it across the literal boundary,
	// and both tiers observe the same value on every call.
	if err := rt.Bind("give", func(f func(string)) { f("x") }); err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := rt.Bind("sink", func(s string) { got = append(got, s) }); err != nil {
		t.Fatal(err)
	}
	src := `give(func(s) { twice(func() { sink(s) }) });`
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	p, err := rt.compiler.compileProgram(prog)
	if err != nil {
		t.Fatal(err)
	}
	runners := map[string]CompiledFunc{"reflect": p.run}
	if jp, err := jitCompileProgram(p); err == nil {
		runners["jit"] = jp.run
	}
	for name, fn := range runners {
		got = nil
		if _, err := fn(t.Context(), nil, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != 2 || got[0] != "x" || got[1] != "x" {
			t.Fatalf("%s: sink saw %v, want [x x]", name, got)
		}
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
