package gozero

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestCompiler checks the compile-time validation the type promises: a
// literal that does not fit the parameter type fails at Compile, before
// anything runs.
func TestCompiler(t *testing.T) {
	rt := newRuntime(t)
	prog, err := (&Parser{}).Parse(`return NewRequest(42, "https://example.com");`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.compiler.Compile(prog); err == nil {
		t.Error("expected an int literal in a string parameter to fail at compile time")
	}
	prog, err = (&Parser{}).Parse(`return Missing("GET");`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.compiler.Compile(prog); err == nil {
		t.Error("expected an unknown binding to fail at compile time")
	}
}

func TestCompiler_Compile(t *testing.T) {
	rt := newRuntime(t)

	// A flat call takes the single-statement path.
	prog, err := (&Parser{}).Parse(`return NewRequest("GET", "https://example.com/flat");`)
	if err != nil {
		t.Fatal(err)
	}
	fn, err := rt.compiler.Compile(prog)
	if err != nil {
		t.Fatal(err)
	}
	res, err := fn(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req := res.(*http.Request); req.URL.Path != "/flat" {
		t.Errorf("path = %q, want /flat", req.URL.Path)
	}

	// A two-statement program compiles to the VM and runs the same.
	prog, err = (&Parser{}).Parse(`req := NewRequest("GET", "https://example.com/vm"); return req;`)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = rt.compiler.Compile(prog)
	if err != nil {
		t.Fatal(err)
	}
	res, err = fn(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req := res.(*http.Request); req.URL.Path != "/vm" {
		t.Errorf("path = %q, want /vm", req.URL.Path)
	}
}

// TestCompilerFlatReturnArgKinds pins the fix for a compile-time
// panic: a flat "return f(x);" with a nil or bool argument
// reached the single-statement compiler, which handles only strings,
// numbers and names and called Type on an invalid reflect.Value for
// anything else. The table walks every argument kind the parser
// produces, plus the spread forms: each one either runs or reports a
// compile error, and none may panic.
func TestCompilerFlatReturnArgKinds(t *testing.T) {
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"wantAny": func(v any) any { return v },
		"give":    func() string { return "g" },
		"Parse":   url.Parse,
		"Join":    path.Join,
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	stack := map[string]any{"x": 7, "xs": []string{"a", "b"}}
	for name, tc := range map[string]struct {
		src     string
		want    any
		wantErr bool
	}{
		"string":     {src: `return wantAny("s");`, want: "s"},
		"int":        {src: `return wantAny(5);`, want: int64(5)},
		"float":      {src: `return wantAny(2.5);`, want: 2.5},
		"bool":       {src: `return wantAny(true);`, want: true},
		"nil":        {src: `return wantAny(nil);`, want: nil},
		"var":        {src: `return wantAny(x);`, want: 7},
		"call":       {src: `return wantAny(give());`, want: "g"},
		"struct":     {src: `return wantAny(url.URL{Path: "/x"});`, want: url.URL{Path: "/x"}},
		"struct ptr": {src: `return wantAny(&url.URL{Path: "/x"});`, want: &url.URL{Path: "/x"}},
		"spread":     {src: `return Join(xs...);`, want: "a/b"},
		// A path needs a program-bound base. A spread needs a
		// variadic parameter; both report at compile time.
		"path":           {src: `return wantAny(a.b);`, wantErr: true},
		"spread non-var": {src: `return wantAny(xs...);`, wantErr: true},
	} {
		v, err := rt.Eval[any](tc.src, stack)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected a compile error for %q", name, tc.src)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(v, tc.want) {
			t.Errorf("%s: got %#v, want %#v", name, v, tc.want)
		}
	}
}

var bytesNewBufferString = bytes.NewBufferString

// testFixtures is the handwritten side of the comparison: each method
// is the Go a fixture program stands for, doing the same work and the
// same assertions. The benchmark builds ctx once before the measured
// loop, the same way as the vm side's execution context: a per-iteration
// context.WithValue on one side only showed up as a one-allocation
// difference that belonged to the harness, not to either
// implementation.
type testFixtures struct {
	ctx context.Context
}

func (f *testFixtures) testHTTP(tb testing.TB) {
	req, err := http.NewRequestWithContext(f.ctx, "GET", "https://example.com/a/b", nil)
	if err != nil {
		tb.Fatal(err)
	}
	assertEqual(tb, "GET", req.Method, "")
	assertEqual(tb, "/a/b", req.URL.Path, "")

	req.Method = "POST"
	assertEqual(tb, "POST", req.Method, "")

	req.Host = "override.example.com"
	assertEqual(tb, "override.example.com", req.Host, "")

	ctx := req.Context()
	v, _ := ctx.Value(fixtureCtxKey{}).(string)
	assertEqual(tb, "fixture", v, "")
}

func (f *testFixtures) testURL(tb testing.TB) {
	u, err := url.Parse("https://user@example.com/p/q?x=1")
	if err != nil {
		tb.Fatal(err)
	}
	assertEqual(tb, "https", u.Scheme, "")
	assertEqual(tb, "example.com", u.Host, "")
	assertEqual(tb, "/p/q", u.Path, "")
	assertEqual(tb, "x=1", u.RawQuery, "")

	u.Path = "/rewritten"
	assertEqual(tb, "https://user@example.com/rewritten?x=1", u.String(), "")

	vals, err := url.ParseQuery("a=1&b=2")
	if err != nil {
		tb.Fatal(err)
	}
	assertEqual(tb, "1", vals.Get("a"), "")
	assertEqual(tb, "2", vals.Get("b"), "")
}

func (f *testFixtures) testJSON(tb testing.TB) {
	buf := bytesNewBufferString("")
	enc := json.NewEncoder(buf)
	if err := enc.Encode(42); err != nil {
		tb.Fatal(err)
	}
	assertEqual(tb, "42\n", buf.String(), "")

	buf2 := bytesNewBufferString("")
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		tb.Fatal(err)
	}
	if err := json.NewEncoder(buf2).Encode(req.Cookies()); err != nil {
		tb.Fatal(err)
	}
	assertEqual(tb, "[]\n", buf2.String(), "")
}

func (f *testFixtures) testFmt(tb testing.TB) {
	var n int64
	n = 7
	s := fmt.Sprintf("n=%d ok=%v", n, true)
	assertEqual(tb, "n=7 ok=true", s, "")

	assertEqual(tb, "a b", fmt.Sprint("a", " ", "b"), "")
}

func (f *testFixtures) testTypes(tb testing.TB) {
	var count int32
	count = 41
	assertEqual(tb, "41", fmt.Sprintf("%d", count), "")

	x := 2.5
	assertEqual(tb, "2.5", fmt.Sprintf("%v", x), "")

	var u url.URL
	assertEqual(tb, "", u.Path, "")
	assertTrue(tb, true, "")

	y := int32(7)
	assertEqual(tb, "int32", fmt.Sprintf("%T", y), "")
	assertEqual(tb, "7", fmt.Sprintf("%d", y), "")

	// The fixture names it f; the receiver holds that name here.
	fl := float32(1.5)
	assertEqual(tb, "float32", fmt.Sprintf("%T", fl), "")

	y = 9
	assertEqual(tb, "int32", fmt.Sprintf("%T", y), "")
}

func (f *testFixtures) testStructs(tb testing.TB) {
	u := url.URL{Scheme: "https", Host: "example.com", Path: "/a"}
	assertEqual(tb, "https", u.Scheme, "")
	assertEqual(tb, "/a", u.Path, "")

	p := &url.URL{Scheme: "https", Host: "h", Path: "/p"}
	assertEqual(tb, "https://h/p", p.String(), "")

	// The fixture's positional url.URL{"https"} fills Scheme. Because
	// Go requires every field positionally, the keyed form stands in.
	s := url.URL{Scheme: "https"}
	assertEqual(tb, "https", s.Scheme, "")

	r := &http.Request{Method: "POST", URL: &url.URL{Path: "/n"}, ProtoMajor: 1}
	assertEqual(tb, "POST", r.Method, "")
	assertEqual(tb, "/n", r.URL.Path, "")
	assertEqual(tb, "int", fmt.Sprintf("%T", r.ProtoMajor), "")

	cu, err := url.Parse("https://h/c")
	if err != nil {
		tb.Fatal(err)
	}
	c := &http.Request{URL: cu}
	assertEqual(tb, "/c", c.URL.Path, "")

	w := url.URL{}
	w.Path = "/w"
	assertEqual(tb, "/w", w.Path, "")

	assertEqual(tb, "/arg", fmt.Sprint(&url.URL{Path: "/arg"}), "")

	m := &url.URL{
		Scheme: "https",
		Host:   "h",
		Path:   "/m",
	}
	assertEqual(tb, "https://h/m", m.String(), "")
}

func (f *testFixtures) testVariadic(tb testing.TB) {
	parts := strings.Fields("a b c")
	joined := path.Join(parts...)
	assertEqual(tb, "a/b/c", joined, "path.Join over spread fields")
}

// testVars mirrors the vars fixture. Every name the program reads is a
// local here, the same shape a value binding has on the other side: a
// copy the run owns, seeded from the value the host bound.
func (f *testFixtures) testVars(tb testing.TB) {
	argv := []string{"prog", "-v"}
	cursor := 0
	frozen := []string{"kept"}
	counter := fixtureCounter{N: 1}
	cfg := fixtureCfg{Fn: func() string { return "from a field" }}

	assertEqual(tb, "3600", fmt.Sprintf("%d", int64(time.Hour/time.Second)), "")
	assertEqual(tb, "EOF", io.EOF.Error(), "")

	assertEqual(tb, "kept", frozen[0], "")
	frozen = []string{"replaced", "by a literal"}
	assertEqual(tb, "2", fmt.Sprintf("%d", len(frozen)), "")
	assertEqual(tb, "replaced", frozen[0], "")

	frozen = append(frozen, "appended")
	assertEqual(tb, "3", fmt.Sprintf("%d", len(frozen)), "")
	assertEqual(tb, "appended", frozen[2], "")

	assertEqual(tb, "prog", argv[0], "")
	assertEqual(tb, "2", fmt.Sprintf("%d", len(argv)), "")

	argv = []string{"1", "2", "3"}
	assertEqual(tb, "3", fmt.Sprintf("%d", len(argv)), "")
	assertEqual(tb, "1", argv[0], "")

	argv = append(argv, "4")
	assertEqual(tb, "4", fmt.Sprintf("%d", len(argv)), "")

	cursor = 41
	cursor++
	assertEqual(tb, "42", fmt.Sprintf("%d", cursor), "")

	seven := 7
	p := &seven
	assertEqual(tb, "7", fmt.Sprintf("%d", *p), "")
	*p = 8
	assertEqual(tb, "8", fmt.Sprintf("%d", *p), "")

	assertEqual(tb, "1", fmt.Sprintf("%d", counter.N), "")
	counter.Bump()
	counter.Bump()
	assertEqual(tb, "3", fmt.Sprintf("%d", counter.N), "")

	assertEqual(tb, "from a field", cfg.Fn(), "")

	xs := []string{"a", "b"}
	assertEqual(tb, "2", fmt.Sprintf("%d", len(xs)), "")
	assertEqual(tb, "b", xs[1], "")
	assertEqual(tb, "a/b", path.Join(xs...), "")
}

// BenchmarkFixtures runs each fixture program against its handwritten
// method: same work, same assertions, same context source. The VM side
// compiles once outside the loop, which is the compile-once/run-many
// shape the cache exists for.
func BenchmarkFixtures(b *testing.B) {
	native := map[string]func(*testFixtures, testing.TB){
		"http":     (*testFixtures).testHTTP,
		"url":      (*testFixtures).testURL,
		"json":     (*testFixtures).testJSON,
		"fmt":      (*testFixtures).testFmt,
		"structs":  (*testFixtures).testStructs,
		"types":    (*testFixtures).testTypes,
		"variadic": (*testFixtures).testVariadic,
		"vars":     (*testFixtures).testVars,
	}

	files, err := filepath.Glob(filepath.Join("testdata", "*.txt"))
	if err != nil {
		b.Fatal(err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".txt")
		fn, ok := native[name]
		if !ok {
			b.Fatalf("no native mirror for fixture %s", name)
		}
		src, err := os.ReadFile(file)
		if err != nil {
			b.Fatal(err)
		}

		b.Run(name+"/vm", func(b *testing.B) {
			rt := fixtureRuntime(b)
			compiled, err := rt.Compile(string(src))
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.WithValue(b.Context(), fixtureCtxKey{}, "fixture")
			stack := map[string]any{"tb": b}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := compiled.ExecContext[any](ctx, stack); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(name+"/native", func(b *testing.B) {
			f := &testFixtures{ctx: context.WithValue(b.Context(), fixtureCtxKey{}, "fixture")}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				fn(f, b)
			}
		})
	}
}

// BenchmarkFixtureWork isolates the bridge from the assertions: the
// http fixture's work with no assert calls, against the same lines in
// Go. This is the shape the engine is for, and unlike the fixtures it
// reaches the direct-call tier.
func BenchmarkFixtureWork(b *testing.B) {
	const src = `
		req := http.NewRequestWithContext("GET", "https://example.com/a/b");
		req.Method = "POST";
		req.Host = "override.example.com";
		return req;
	`
	rt := fixtureRuntime(b)
	if err := rt.Supports(src); err != nil {
		b.Fatalf("the work program should JIT: %v", err)
	}
	compiled, err := rt.Compile(src)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.WithValue(b.Context(), fixtureCtxKey{}, "fixture")

	var sink *http.Request
	b.Run("vm", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			req, err := compiled.ExecContext[*http.Request](ctx, nil)
			if err != nil {
				b.Fatal(err)
			}
			sink = req
		}
	})
	b.Run("native", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			req, err := http.NewRequestWithContext(ctx, "GET", "https://example.com/a/b", nil)
			if err != nil {
				b.Fatal(err)
			}
			req.Method = "POST"
			req.Host = "override.example.com"
			sink = req
		}
	})
	_ = sink
}
