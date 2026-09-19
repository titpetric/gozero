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
// same assertions. ctx is built once before the measured loop, the
// same way the vm side's execution context is: a per-iteration
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

	// The fixture's positional url.URL{"https"} fills Scheme; Go
	// requires every field positionally, so the keyed form stands in.
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

func (f *testFixtures) testTypedecl(tb testing.TB) {
	type Point struct {
		X int64
		Y int64
	}
	p := Point{X: 3, Y: 4}
	assertEqual(tb, int64(3), p.X, "")
	p.X = 5
	assertEqual(tb, int64(5), p.X, "")

	q := &Point{X: 1}
	assertEqual(tb, int64(1), q.X, "")

	var w Point
	w.Y = 7
	assertEqual(tb, int64(7), w.Y, "")

	// Go resolves local types in order, so Base comes first here; the
	// fixture declares Wrap first to prove declaration order is free.
	type Base struct{ N int64 }
	type Wrap struct {
		Inner Base
		M     int64
	}
	b := Wrap{Inner: Base{N: 3}, M: 4}
	assertEqual(tb, int64(3), b.Inner.N, "")
	assertEqual(tb, int64(4), b.M, "")

	buf := bytesNewBufferString("")
	if err := json.NewEncoder(buf).Encode(Point{X: 1, Y: 2}); err != nil {
		tb.Fatal(err)
	}
	assertEqual(tb, "{\"X\":1,\"Y\":2}\n", buf.String(), "")
}

func (f *testFixtures) testReply(tb testing.TB) {
	// Go resolves local types in order; the fixture declares Reply
	// first to prove declaration order stays free with tags.
	type Status struct {
		Code int64  `json:"code"`
		Text string `json:"text"`
	}
	type Reply struct {
		Status Status `json:"status"`
		Count  int64  `json:"count"`
		Note   string `json:"note,omitempty"`
		Skip   string `json:"-"`
	}
	r := Reply{Status: Status{Code: 200, Text: "ok"}, Count: 2, Skip: "never"}
	assertEqual(tb, int64(200), r.Status.Code, "")
	assertEqual(tb, "ok", r.Status.Text, "")

	buf := bytesNewBufferString("")
	if err := json.NewEncoder(buf).Encode(r); err != nil {
		tb.Fatal(err)
	}
	assertEqual(tb, "{\"status\":{\"code\":200,\"text\":\"ok\"},\"count\":2}\n", buf.String(), "")

	n := Reply{Status: Status{Code: 404, Text: "gone"}, Note: "retry"}
	buf2 := bytesNewBufferString("")
	if err := json.NewEncoder(buf2).Encode(n); err != nil {
		tb.Fatal(err)
	}
	assertEqual(tb, "{\"status\":{\"code\":404,\"text\":\"gone\"},\"count\":0,\"note\":\"retry\"}\n", buf2.String(), "")

	var d Reply
	d.Note = "later"
	assertEqual(tb, "later", d.Note, "")
}

func (f *testFixtures) testShape(tb testing.TB) {
	type Size struct {
		// W and H share one spelling, as in Go.
		W, H int64
		Area int64 `json:"area,omitempty"`
	}
	s := Size{W: 3, H: 4, Area: 12}
	assertEqual(tb, int64(3), s.W, "")
	assertEqual(tb, int64(4), s.H, "")

	var u Size
	u.W = 7
	assertEqual(tb, int64(7), u.W, "")

	buf := bytesNewBufferString("")
	if err := json.NewEncoder(buf).Encode(s); err != nil {
		tb.Fatal(err)
	}
	assertEqual(tb, "{\"W\":3,\"H\":4,\"area\":12}\n", buf.String(), "")
}

func (f *testFixtures) testVariadic(tb testing.TB) {
	parts := strings.Fields("a b c")
	joined := path.Join(parts...)
	assertEqual(tb, "a/b/c", joined, "path.Join over spread fields")
}

func (f *testFixtures) testChannels(tb testing.TB) {
	c := chanOf("a", "b")
	v := <-c
	assertEqual(tb, "a", v, "")

	w := <-c
	assertEqual(tb, "b", w, "")

	c <- "sent"
	r := <-c
	assertEqual(tb, "sent", r, "")

	var d chan string
	d = chanOf("typed")
	s := <-d
	assertEqual(tb, "typed", s, "")
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
		"reply":    (*testFixtures).testReply,
		"shape":    (*testFixtures).testShape,
		"structs":  (*testFixtures).testStructs,
		"typedecl": (*testFixtures).testTypedecl,
		"types":    (*testFixtures).testTypes,
		"variadic": (*testFixtures).testVariadic,
		"channels": (*testFixtures).testChannels,
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
			f := &testFixtures{ctx: context.WithValue(b.Context(), fixtureCtxKey{}, "fixture")}
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
	if err := rt.Bind("chanOf", chanOf); err != nil {
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
