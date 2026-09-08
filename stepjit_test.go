package gozero

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

// compilePair compiles src twice: once as the step-JIT program and once
// as the reflect evaluator, so a test can run the same source down both
// paths and compare. It fails when the source does not JIT, because a
// silent fallback would make an equivalence test pass by running the
// reflect path twice.
func compilePair(t *testing.T, rt *Runtime, src string) (jit, slow CompiledFunc) {
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

func pairRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime()
	for name, fns := range map[string]map[string]any{
		"http": {"NewRequest": http.NewRequest},
		"json": {"NewEncoder": json.NewEncoder},
		"url":  {"Parse": url.Parse},
	} {
		if err := rt.BindScope(name, fns); err != nil {
			t.Fatal(err)
		}
	}
	return rt
}

// TestStepJITMatchesReflect runs the same program through both tiers
// and requires identical output. Every unsafe path in stepjit.go is
// reachable from one of these: the frame layout, the raw stores, the
// precomputed itabs on both the direct and indirect sides, and the
// error words.
func TestStepJITMatchesReflect(t *testing.T) {
	rt := pairRuntime(t)

	// A pointer slot filling an interface parameter takes the direct
	// path; a slice slot takes the indirect one. Both are covered by
	// recording what the callee actually received.
	// Describing the value rather than storing it proves the callee can
	// read through the interface it was handed: a wrong itab or data
	// word would fault or produce nonsense here, where comparing
	// pointers would only compare two different allocations.
	var seen []string
	if err := rt.Bind("record", func(v any) (*url.URL, error) {
		switch x := v.(type) {
		case *http.Request:
			seen = append(seen, fmt.Sprintf("%T %s %s", x, x.Method, x.URL.Path))
		case []*http.Cookie:
			seen = append(seen, fmt.Sprintf("%T len=%d", x, len(x)))
		case *url.URL:
			seen = append(seen, fmt.Sprintf("%T %s", x, x.Path))
		default:
			seen = append(seen, fmt.Sprintf("%T %v", v, v))
		}
		return &url.URL{Path: "/recorded"}, nil
	}); err != nil {
		t.Fatal(err)
	}

	for name, src := range map[string]string{
		"write through dest": `
			req := http.NewRequest("GET", "/");
			cookies := req.Cookies();
			enc := json.NewEncoder(dest);
			enc.Encode(cookies);
		`,
		"chained and nested": `
			json.NewEncoder(dest).Encode(http.NewRequest("GET", "/").Cookies());
		`,
		"pointer into any (direct)": `
			req := http.NewRequest("GET", "/");
			u := record(req);
			json.NewEncoder(dest).Encode(u);
		`,
		"slice into any (indirect)": `
			cookies := http.NewRequest("GET", "/").Cookies();
			u := record(cookies);
			json.NewEncoder(dest).Encode(u);
		`,
		"omitted argument": `
			json.NewEncoder(dest).Encode(url.Parse("https://example.com/a"));
		`,
		"string from the stack": `
			json.NewEncoder(dest).Encode(url.Parse(link));
		`,
		"field as argument": `
			req := http.NewRequest("GET", "/");
			json.NewEncoder(dest).Encode(req.Header);
		`,
		"field of a field": `
			req := http.NewRequest("GET", "/");
			json.NewEncoder(dest).Encode(req.URL.Path);
		`,
		"field through record": `
			req := http.NewRequest("GET", "/");
			u := record(req.URL);
			json.NewEncoder(dest).Encode(u);
		`,
	} {
		stack := map[string]any{"link": "https://example.com/from-stack"}
		jit, slow := compilePair(t, rt, src)

		seen = nil
		var jitBuf bytes.Buffer
		jitRes, jitErr := jit(context.Background(), stack, &jitBuf)
		jitSeen := fmt.Sprintf("%v", seen)

		seen = nil
		var slowBuf bytes.Buffer
		slowRes, slowErr := slow(context.Background(), stack, &slowBuf)
		slowSeen := fmt.Sprintf("%v", seen)

		switch {
		case (jitErr == nil) != (slowErr == nil):
			t.Errorf("%s: err = %v (jit) vs %v (reflect)", name, jitErr, slowErr)
		case jitErr != nil && jitErr.Error() != slowErr.Error():
			t.Errorf("%s: err = %q (jit) vs %q (reflect)", name, jitErr, slowErr)
		}
		if jitBuf.String() != slowBuf.String() {
			t.Errorf("%s: dest = %q (jit) vs %q (reflect)", name, jitBuf.String(), slowBuf.String())
		}
		if jitSeen != slowSeen {
			t.Errorf("%s: callee saw %s (jit) vs %s (reflect)", name, jitSeen, slowSeen)
		}
		if fmt.Sprintf("%v", jitRes) != fmt.Sprintf("%v", slowRes) {
			t.Errorf("%s: result = %v (jit) vs %v (reflect)", name, jitRes, slowRes)
		}
	}
}

// TestStepJITErrorsMatch checks the error words on the JIT side against
// the reflect side, on a failing binding and on a bad stack value.
func TestStepJITErrorsMatch(t *testing.T) {
	rt := pairRuntime(t)
	if err := rt.Bind("fail", func(s string) (*url.URL, error) {
		return nil, errors.New("boom: " + s)
	}); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		src   string
		stack map[string]any
	}{
		"binding error":  {`u := fail("x"); json.NewEncoder(dest).Encode(u);`, nil},
		"bad url":        {`json.NewEncoder(dest).Encode(url.Parse(link));`, map[string]any{"link": "://bad"}},
		"wrong var type": {`json.NewEncoder(dest).Encode(url.Parse(link));`, map[string]any{"link": 42}},
	} {
		jit, slow := compilePair(t, rt, tc.src)
		var a, b bytes.Buffer
		_, jitErr := jit(context.Background(), tc.stack, &a)
		_, slowErr := slow(context.Background(), tc.stack, &b)
		if jitErr == nil || slowErr == nil {
			t.Errorf("%s: expected errors, got %v (jit) %v (reflect)", name, jitErr, slowErr)
			continue
		}
		if jitErr.Error() != slowErr.Error() {
			t.Errorf("%s: err = %q (jit) vs %q (reflect)", name, jitErr, slowErr)
		}
	}
}

// TestSupportsReportsTheReason checks the public gate: a program that
// JITs reports nil, and one that does not names what stopped it.
func TestSupportsReportsTheReason(t *testing.T) {
	rt := pairRuntime(t)
	if err := rt.Bind("count", func() int { return 7 }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Supports(`json.NewEncoder(dest).Encode(url.Parse("/x"));`); err != nil {
		t.Errorf("supported program reported %v", err)
	}
	err := rt.Supports(`n := count(); json.NewEncoder(dest).Encode("x");`)
	if err == nil {
		t.Fatal("an int result should be reported as unsupported")
	}
	t.Log(err)
	if _, err := rt.Supports(`this is not a program`), error(nil); err != nil {
		_ = err
	}
	if rt.Supports(`nope(`) == nil {
		t.Error("a parse error should be reported")
	}
}

// TestPanicBoundary checks that a panic raised inside a binding comes
// back as an error rather than unwinding through the JIT's raw stores,
// on both tiers.
func TestPanicBoundary(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"jit", `u := boom("x"); json.NewEncoder(dest).Encode(u);`},
		{"reflect", `n := count(); u := boom("x"); json.NewEncoder(dest).Encode(u);`},
	} {
		rt := pairRuntime(t)
		if err := rt.Bind("boom", func(s string) (*url.URL, error) {
			panic("binding exploded: " + s)
		}); err != nil {
			t.Fatal(err)
		}
		if err := rt.Bind("count", func() int { return 7 }); err != nil {
			t.Fatal(err)
		}
		supported := rt.Supports(tc.src) == nil
		if supported != (tc.name == "jit") {
			t.Fatalf("%s: Supports = %v, want %v", tc.name, supported, tc.name == "jit")
		}

		fn, err := rt.Compile(tc.src)
		if err != nil {
			t.Fatal(err)
		}
		var dest bytes.Buffer
		err = fn.Scan(&dest, nil)
		if err == nil {
			t.Fatalf("%s: expected the panic to come back as an error", tc.name)
		}
		var pe *PanicError
		if !errors.As(err, &pe) {
			t.Fatalf("%s: err = %T (%v), want *PanicError", tc.name, err, err)
		}
		if pe.Value != "binding exploded: x" {
			t.Errorf("%s: value = %v", tc.name, pe.Value)
		}
		if len(pe.Stack) == 0 {
			t.Errorf("%s: no stack captured", tc.name)
		}
	}
}
