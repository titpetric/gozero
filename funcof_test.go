package gozero

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// TestFuncOfHandleFunc is the design note's motivating case: a mux
// route served by a materialized program. The handler reads both
// parameters - r through a binding, w as fmt.Fprint's writer - and the
// program runs on the direct tier, pinned by Supports. The method
// binding takes any rather than *http.Request because a stack value
// fills interface parameters on the direct tier, not pointer ones; a
// typed binding would bridge that one call through reflect.
func TestFuncOfHandleFunc(t *testing.T) {
	rt := fixtureRuntime(t)
	if err := rt.Bind("method", func(v any) string { return v.(*http.Request).Method }); err != nil {
		t.Fatal(err)
	}
	const src = `
		m := method(r);
		fmt.Fprint(w, "ok ", m);
	`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("the handler program should JIT: %v", err)
	}
	h, err := rt.FuncOf[http.HandlerFunc](src, "w", "r")
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", h)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if got := rec.Body.String(); got != "ok GET" {
		t.Fatalf("body = %q, want %q", got, "ok GET")
	}
}

// TestFuncOfResultAndError pins the result mapping: the program's
// return value fills the non-error result, and the program's error
// travels out through the trailing error.
func TestFuncOfResultAndError(t *testing.T) {
	rt := fixtureRuntime(t)
	if err := rt.Bind("fail", func(msg string) (string, error) { return "", errors.New(msg) }); err != nil {
		t.Fatal(err)
	}

	greet, err := rt.FuncOf[func(string) (string, error)](`s := fmt.Sprintf("hello %s", name); return s;`, "name")
	if err != nil {
		t.Fatal(err)
	}
	got, err := greet("mux")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello mux" {
		t.Fatalf("got %q, want %q", got, "hello mux")
	}

	boom, err := rt.FuncOf[func(string) (string, error)](`s := fail(msg); return s;`, "msg")
	if err != nil {
		t.Fatal(err)
	}
	got, err = boom("kaput")
	if err == nil || err.Error() != "kaput" {
		t.Fatalf("err = %v, want kaput", err)
	}
	if got != "" {
		t.Fatalf("a failing call must zero its result, got %q", got)
	}
}

// TestFuncOfDefaultNames pins the naming rule: with no names given,
// parameters are arg0..argN-1 by position.
func TestFuncOfDefaultNames(t *testing.T) {
	rt := fixtureRuntime(t)
	join, err := rt.FuncOf[func(string, string) string](`s := fmt.Sprint(arg1, "/", arg0); return s;`)
	if err != nil {
		t.Fatal(err)
	}
	if got := join("a", "b"); got != "b/a" {
		t.Fatalf("got %q, want %q", got, "b/a")
	}
}

// TestFuncOfPanicsWithoutErrorResult pins the failure rule for a
// signature with no error channel: the program's error arrives as a
// panic, the way a Go func without an error result fails.
func TestFuncOfPanicsWithoutErrorResult(t *testing.T) {
	rt := fixtureRuntime(t)
	if err := rt.Bind("fail", func(msg string) (string, error) { return "", errors.New(msg) }); err != nil {
		t.Fatal(err)
	}
	f, err := rt.FuncOf[func(string) string](`s := fail(arg0); return s;`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		r := recover()
		err, ok := r.(error)
		if !ok || err.Error() != "kaput" {
			t.Fatalf("recovered %v, want the kaput error", r)
		}
	}()
	f("kaput")
	t.Fatal("the call should have panicked")
}

// TestFuncOfContext pins the context rule: a context.Context parameter
// becomes the run's execution context and auto-fills the context
// parameter of a binding, exactly as ExecContext would.
func TestFuncOfContext(t *testing.T) {
	rt := fixtureRuntime(t)
	f, err := rt.FuncOf[func(context.Context) (string, error)](`v := ctxValue(); return v;`, "ctx")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), fixtureCtxKey{}, "flowed")
	got, err := f(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "flowed" {
		t.Fatalf("got %q, want %q", got, "flowed")
	}
}

// TestFuncOfDeclines names every signature rule FuncOf enforces at
// materialization time.
func TestFuncOfDeclines(t *testing.T) {
	rt := fixtureRuntime(t)
	const src = `s := fmt.Sprint("x"); return s;`

	if _, err := rt.FuncOf[int](src); err == nil || !strings.Contains(err.Error(), "want a func type") {
		t.Fatalf("non-func F: %v", err)
	}
	if _, err := rt.FuncOf[func(...string) string](src); err == nil || !strings.Contains(err.Error(), "variadic") {
		t.Fatalf("variadic F: %v", err)
	}
	if _, err := rt.FuncOf[func() (string, int)](src); err == nil || !strings.Contains(err.Error(), "results besides error") {
		t.Fatalf("two results: %v", err)
	}
	if _, err := rt.FuncOf[func() (error, string)](src); err == nil || !strings.Contains(err.Error(), "must be last") {
		t.Fatalf("error not last: %v", err)
	}
	if _, err := rt.FuncOf[func(string) string](src, "a", "b"); err == nil || !strings.Contains(err.Error(), "2 names for the 1 parameters") {
		t.Fatalf("name count: %v", err)
	}
	if _, err := rt.FuncOf[func(string, string) string](src, "a", "a"); err == nil || !strings.Contains(err.Error(), "repeats") {
		t.Fatalf("repeated name: %v", err)
	}
	if _, err := rt.FuncOf[func(string) string](src, ""); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := rt.FuncOf[func(string) string](src, "dest"); err == nil || !strings.Contains(err.Error(), "shadows") {
		t.Fatalf("reserved name: %v", err)
	}
	if _, err := rt.FuncOf[func(string) string](src, "fmt"); err == nil || !strings.Contains(err.Error(), "shadows") {
		t.Fatalf("binding shadow: %v", err)
	}
	// The parameter rule and the assignment rule read one predicate,
	// so every word the statement grammar took is out of bounds here
	// too, without funcof.go carrying a second list to keep current.
	for _, kw := range []string{"if", "else", "for", "range", "break", "continue", "type", "struct", "func", "var", "return", "true", "false", "nil"} {
		if _, err := rt.FuncOf[func(string) string](src, kw); err == nil || !strings.Contains(err.Error(), "shadows") {
			t.Fatalf("keyword %q: %v", kw, err)
		}
	}
	if _, err := rt.FuncOf[func() string](`x := ;`); err == nil {
		t.Fatal("a parse error must propagate")
	}
	if _, err := rt.FuncOf[func() (string, error)](src); err != nil {
		t.Fatalf("a value the program returns fills the result: %v", err)
	}
}

// TestFuncOfResultMismatch pins the run-time type rule: a program
// value that does not fit the result reports through the error result
// rather than panicking, when there is one.
func TestFuncOfResultMismatch(t *testing.T) {
	rt := fixtureRuntime(t)
	f, err := rt.FuncOf[func() (int64, error)](`s := fmt.Sprint("x"); return s;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f(); err == nil || !strings.Contains(err.Error(), "returned string, want int64") {
		t.Fatalf("err = %v, want the mismatch", err)
	}
}

// TestFuncOfTiersAgree materializes the same signature over both
// tiers of the same program and requires identical results, observable
// effects, and errors. compilePair fails the test when the source does
// not JIT, so the direct tier is really exercised, the new IL_i64E
// shape included.
func TestFuncOfTiersAgree(t *testing.T) {
	rt := pairRuntime(t)
	if err := rt.Bind("fmt.Sprintf", fmt.Sprintf); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("fmt.Fprint", fmt.Fprint); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("fail", func(msg string) (string, error) { return "", errors.New(msg) }); err != nil {
		t.Fatal(err)
	}

	t.Run("value", func(t *testing.T) {
		jit, slow := compilePair(t, rt, `s := fmt.Sprintf("got %s", v); return s;`)
		ft := reflect.TypeFor[func(string) (string, error)]()
		for tier, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
			fv, err := materialize(ft, fn, []string{"v"})
			if err != nil {
				t.Fatalf("%s: %v", tier, err)
			}
			got, err := fv.Interface().(func(string) (string, error))("x")
			if err != nil {
				t.Fatalf("%s: %v", tier, err)
			}
			if got != "got x" {
				t.Fatalf("%s: got %q, want %q", tier, got, "got x")
			}
		}
	})

	t.Run("effect", func(t *testing.T) {
		jit, slow := compilePair(t, rt, `fmt.Fprint(w, "a", v);`)
		ft := reflect.TypeFor[func(*bytes.Buffer, string)]()
		for tier, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
			fv, err := materialize(ft, fn, []string{"w", "v"})
			if err != nil {
				t.Fatalf("%s: %v", tier, err)
			}
			var buf bytes.Buffer
			fv.Interface().(func(*bytes.Buffer, string))(&buf, "b")
			if got := buf.String(); got != "ab" {
				t.Fatalf("%s: wrote %q, want %q", tier, got, "ab")
			}
		}
	})

	t.Run("error", func(t *testing.T) {
		jit, slow := compilePair(t, rt, `s := fail(msg); return s;`)
		ft := reflect.TypeFor[func(string) (string, error)]()
		for tier, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
			fv, err := materialize(ft, fn, []string{"msg"})
			if err != nil {
				t.Fatalf("%s: %v", tier, err)
			}
			got, err := fv.Interface().(func(string) (string, error))("kaput")
			if err == nil || err.Error() != "kaput" {
				t.Fatalf("%s: err = %v, want kaput", tier, err)
			}
			if got != "" {
				t.Fatalf("%s: result = %q, want zero", tier, got)
			}
		}
	})
}

// TestFuncOfConcurrent runs one materialized func from many
// goroutines: the pooled per-call stack maps must never mix arguments
// between calls.
func TestFuncOfConcurrent(t *testing.T) {
	rt := fixtureRuntime(t)
	echo, err := rt.FuncOf[func(string) (string, error)](`s := fmt.Sprint("<", v, ">"); return s;`, "v")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			want := fmt.Sprint("<", g, ">")
			for i := 0; i < 200; i++ {
				got, err := echo(fmt.Sprint(g))
				if err != nil {
					t.Error(err)
					return
				}
				if got != want {
					t.Errorf("got %q, want %q", got, want)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
