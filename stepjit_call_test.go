package gozero

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestBridgedCallStaysInTheTree replaces the whole-program fallback
// test: a call outside the shape table no longer sends the program to
// the reflect evaluator, it compiles to a reflect bridge and its
// neighbours stay direct calls. Supports reports the bridge, and the
// bridged call still runs correctly.
func TestBridgedCallStaysInTheTree(t *testing.T) {
	rt := pairRuntime(t)
	// Five string parameters is a shape the table does not carry, so
	// the call bridges while its neighbours stay direct.
	if err := rt.Bind("join5", func(a, b, c, d, e string) (*url.URL, error) {
		return &url.URL{Path: "/" + a + b + c + d + e}, nil
	}); err != nil {
		t.Fatal(err)
	}
	const src = `
		u := join5("b", "r", "i", "d", "ge");
		json.NewEncoder(dest).Encode(u.Path);
	`
	err := rt.Supports(src)
	if err == nil {
		t.Fatal("a five string call should be reported as bridged")
	}
	if !strings.Contains(err.Error(), "bridge") || !strings.Contains(err.Error(), "join5") {
		t.Fatalf("Supports = %v, want it to name the bridged call", err)
	}

	prog, perr := (&Parser{}).Parse(src)
	if perr != nil {
		t.Fatal(perr)
	}
	p, perr := rt.compiler.compileProgram(prog)
	if perr != nil {
		t.Fatal(perr)
	}
	jp, jerr := jitCompileProgram(p)
	if jerr != nil {
		t.Fatalf("the program should still compile to the tree: %v", jerr)
	}
	if len(jp.bridged) == 0 {
		t.Fatal("expected a bridged call")
	}

	var dest bytes.Buffer
	if _, err := jp.run(context.Background(), nil, &dest); err != nil {
		t.Fatal(err)
	}
	if got, want := dest.String(), "\"/bridge\"\n"; got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}
}

// TestAllocationBudget pins the allocation count of the compiled
// program against the native code it stands for, so a regression fails
// the suite rather than only showing up in a benchmark.
func TestAllocationBudget(t *testing.T) {
	rt := pairRuntime(t)
	if err := rt.Supports(vmProgramSrc); err != nil {
		t.Fatalf("the benchmark program must JIT: %v", err)
	}
	fn, err := rt.Compile(vmProgramSrc)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	got := testing.AllocsPerRun(200, func() {
		buf.Reset()
		if err := fn.Scan(&buf, nil); err != nil {
			panic(err)
		}
	})
	want := testing.AllocsPerRun(200, func() {
		buf.Reset()
		req, _ := http.NewRequest("GET", "/", nil)
		_ = json.NewEncoder(&buf).Encode(req.Cookies())
	})
	// The budget is native plus one. The extra is materialising the
	// cookie slice for the interface Encode takes: the Go compiler can
	// prove that value does not outlive the call and keep it on the
	// stack, while the JIT hands it to a func it reinterpreted and has
	// to assume it escapes. Everything else matches.
	t.Logf("program %.0f allocations/run, native %.0f", got, want)
	if got > want+1 {
		t.Errorf("program allocates %.0f/run, native %.0f, budget %.0f", got, want, want+1)
	}
}

// TestBridgeArgKinds sends every argument kind through the reflect
// bridge in one call: the auto-filled context, a stack name, a nested
// call result, a struct field and a literal. Five fixed parameters
// keep the shape out of the table, and the reflect tier must agree.
func TestBridgeArgKinds(t *testing.T) {
	rt := pairRuntime(t)
	if err := rt.Bind("mix5", func(ctx context.Context, a string, u *url.URL, m string, b string) string {
		v, _ := ctx.Value(bridgeCtxKey{}).(string)
		return strings.Join([]string{v, a, u.Path, m, b}, "|")
	}); err != nil {
		t.Fatal(err)
	}
	const src = `
		req := http.NewRequest("GET", "/base")
		s := mix5(fromStack, url.Parse("/nested"), req.Method, "lit")
		json.NewEncoder(dest).Encode(s)
	`
	stack := map[string]any{"fromStack": "stacked"}
	ctx := context.WithValue(context.Background(), bridgeCtxKey{}, "ctxv")

	jit, slow := compilePairBridged(t, rt, src)
	var a, b bytes.Buffer
	if _, err := jit(ctx, stack, &a); err != nil {
		t.Fatal(err)
	}
	if _, err := slow(ctx, stack, &b); err != nil {
		t.Fatal(err)
	}
	want := "\"ctxv|stacked|/nested|GET|lit\"\n"
	if a.String() != want || b.String() != want {
		t.Errorf("dest = %q (jit) vs %q (reflect), want %q", a.String(), b.String(), want)
	}
}

type bridgeCtxKey struct{}

// compilePairBridged is compilePair without the all-direct requirement:
// the program is expected to hold at least one bridged call.
func compilePairBridged(t *testing.T, rt *Runtime, src string) (jit, slow CompiledFunc) {
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
		t.Fatalf("the program should compile to the tree: %v", err)
	}
	if len(jp.bridged) == 0 {
		t.Fatalf("expected a bridged call in %q", src)
	}
	return jp.run, p.run
}

// TestBridgedResultClasses runs one out-of-table call per result class
// the bridge can hand back: string, slice, interface, float and
// integer. Only the pointer result had coverage.
func TestBridgedResultClasses(t *testing.T) {
	rt := pairRuntime(t)
	for name, fn := range map[string]any{
		"brS": func(a, b, c, d, e string) string { return a + b + c + d + e },
		"brL": func(a, b, c, d, e string) []string { return []string{a, e} },
		"brI": func(a, b, c, d, e string) any { return a + e },
		"brF": func(a, b, c, d, e string) float64 { return float64(len(a + e)) },
		"brN": func(a, b, c, d, e string) int64 { return int64(len(a + b)) },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct{ src, want string }{
		"string": {`s := brS("a", "b", "c", "d", "e"); json.NewEncoder(dest).Encode(s)`, "\"abcde\"\n"},
		"slice":  {`l := brL("a", "b", "c", "d", "e"); json.NewEncoder(dest).Encode(l)`, "[\"a\",\"e\"]\n"},
		"iface":  {`v := brI("a", "b", "c", "d", "e"); json.NewEncoder(dest).Encode(v)`, "\"ae\"\n"},
		"float":  {`f := brF("a", "b", "c", "d", "e"); json.NewEncoder(dest).Encode(f)`, "2\n"},
		"int":    {`n := brN("ab", "cd", "e", "f", "g"); json.NewEncoder(dest).Encode(n)`, "4\n"},
	} {
		jit, slow := compilePairBridged(t, rt, tc.src)
		var a, b bytes.Buffer
		if _, err := jit(context.Background(), nil, &a); err != nil {
			t.Errorf("%s: jit: %v", name, err)
			continue
		}
		if _, err := slow(context.Background(), nil, &b); err != nil {
			t.Errorf("%s: reflect: %v", name, err)
			continue
		}
		if a.String() != tc.want || b.String() != tc.want {
			t.Errorf("%s: dest = %q (jit) vs %q (reflect), want %q", name, a.String(), b.String(), tc.want)
		}
	}
}

// TestBridgedSpread pins a variadic spread through the bridge, which is
// the one path that goes through reflect's CallSlice: []int is not a
// pack the table special-cases, so the call bridges, and the spread
// slice arrives as the variadic parameter.
func TestBridgedSpread(t *testing.T) {
	rt := pairRuntime(t)
	if err := rt.Bind("sum", func(base string, ns ...int) int {
		total := len(base)
		for _, n := range ns {
			total += n
		}
		return total
	}); err != nil {
		t.Fatal(err)
	}
	const src = `
		n := sum("ab", xs...)
		json.NewEncoder(dest).Encode(n)
	`
	stack := map[string]any{"xs": []int{1, 2, 3}}
	jit, slow := compilePairBridged(t, rt, src)
	var a, b bytes.Buffer
	if _, err := jit(context.Background(), stack, &a); err != nil {
		t.Fatal(err)
	}
	if _, err := slow(context.Background(), stack, &b); err != nil {
		t.Fatal(err)
	}
	if a.String() != "8\n" || b.String() != "8\n" {
		t.Errorf("dest = %q (jit) vs %q (reflect), want 8", a.String(), b.String())
	}
}
