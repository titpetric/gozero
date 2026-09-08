package gozero

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

var bytesNewBufferString = bytes.NewBufferString

// testFixtures is the handwritten side of the comparison: each method
// is the Go a fixture program stands for, doing the same work and the
// same assertions, with the context taken from tb.Context the way the
// runner derives the VM's execution context from it.
type testFixtures struct{}

func (testFixtures) ctx(tb testing.TB) context.Context {
	return context.WithValue(tb.Context(), fixtureCtxKey{}, "fixture")
}

func (f *testFixtures) testHTTP(tb testing.TB) {
	req, err := http.NewRequestWithContext(f.ctx(tb), "GET", "https://example.com/a/b", nil)
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
}

func (f *testFixtures) testVariadic(tb testing.TB) {
	parts := strings.Fields("a b c")
	joined := path.Join(parts...)
	assertEqual(tb, "a/b/c", joined, "path.Join over spread fields")
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
		"types":    (*testFixtures).testTypes,
		"variadic": (*testFixtures).testVariadic,
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
			rt := newBenchFixtureRuntime(b)
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
			f := &testFixtures{}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				fn(f, b)
			}
		})
	}
}

// newBenchFixtureRuntime mirrors fixtureRuntime for a testing.B.
func newBenchFixtureRuntime(b *testing.B) *Runtime {
	b.Helper()
	rt := NewRuntime()
	for scope, fns := range map[string]map[string]any{
		"http": {
			"NewRequest":            http.NewRequest,
			"NewRequestWithContext": http.NewRequestWithContext,
		},
		"url": {
			"Parse":      url.Parse,
			"ParseQuery": url.ParseQuery,
		},
		"json":    {"NewEncoder": json.NewEncoder},
		"bytes":   {"NewBufferString": bytesNewBufferString},
		"fmt":     {"Sprintf": fmt.Sprintf, "Sprint": fmt.Sprint},
		"strings": {"Fields": strings.Fields},
		"path":    {"Join": path.Join},
		// Equal has no variadic tail, (tb, want, got, message): every
		// parameter has a shape, so an assertion is a direct call. The
		// message is optional the way every trailing argument is,
		// zero-filled to "".
		"assert": {
			"Equal": assertEqual,
			"True":  assertTrue,
		},
	} {
		if err := rt.BindScope(scope, fns); err != nil {
			b.Fatal(err)
		}
	}
	if err := rt.Bind("ctxValue", func(ctx context.Context) string {
		v, _ := ctx.Value(fixtureCtxKey{}).(string)
		return v
	}); err != nil {
		b.Fatal(err)
	}
	return rt
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
	rt := newBenchFixtureRuntime(b)
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
