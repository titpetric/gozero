package gozero

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"
	"time"
)

// The handwritten side of the fixture comparison: one method per
// testdata program, doing the same work and the same assertions in
// Go. BenchmarkFixtures in fixture_bench_test.go pairs each with its
// fixture, and a fixture without a mirror here fails the benchmark.

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

func (f *testFixtures) testIncDec(tb testing.TB) {
	var n int64
	n = 7
	n++
	n++
	n--
	assertEqual(tb, "8", fmt.Sprintf("%d", n), "")

	var c int32
	c = 41
	c++
	assertEqual(tb, "42", fmt.Sprintf("%d", c), "")
	assertEqual(tb, "int32", fmt.Sprintf("%T", c), "")

	var b uint8
	b = 255
	b++
	assertEqual(tb, "0", fmt.Sprintf("%d", b), "")
	b--
	assertEqual(tb, "255", fmt.Sprintf("%d", b), "")

	x := 2.5
	x++
	assertEqual(tb, "3.5", fmt.Sprintf("%v", x), "")
	x--
	x--
	assertEqual(tb, "1.5", fmt.Sprintf("%v", x), "")
}

func (f *testFixtures) testRange(tb testing.TB) {
	parts := strings.Fields("a b c")
	buf := bytesNewBufferString("")
	for _, s := range parts {
		buf.WriteString(s)
	}
	assertEqual(tb, "abc", buf.String(), "")

	c := counterNew()
	for i := range 4 {
		c.Add(int64(i))
	}
	assertEqual(tb, int64(6), c.Sum(), "")

	for range parts {
		c.Add(1)
	}
	assertEqual(tb, int64(9), c.Sum(), "")

	for i := range 10 {
		c.Add(int64(i))
		break
	}
	assertEqual(tb, int64(9), c.Sum(), "")
}

func (f *testFixtures) testFor(tb testing.TB) {
	q := queueOf("a", "b", "c")
	buf := bytesNewBufferString("")
	for q.More() {
		buf.WriteString(q.Next())
	}
	assertEqual(tb, "abc", buf.String(), "")

	c := counterNew()
	var i int64
	for i = 0; i < 4; i++ {
		c.Add(i)
	}
	assertEqual(tb, int64(6), c.Sum(), "")
	assertEqual(tb, int64(4), i, "")

	d := counterNew()
	for j := int64(3); j > 0; j-- {
		d.Add(j)
	}
	assertEqual(tb, int64(6), d.Sum(), "")

	n := strLen("gozero")
	e := counterNew()
	for k := int64(0); k != n; k++ {
		e.Add(1)
		// The fixture's continue still runs the post clause and skips
		// a trailing e.Add(100), so the mirror never runs it either.
	}
	assertEqual(tb, int64(6), e.Sum(), "")

	fc := counterNew()
	var m int64
	for m = 5; m <= 9; m++ {
		fc.Add(m)
		break
	}
	assertEqual(tb, int64(5), fc.Sum(), "")
	assertEqual(tb, int64(5), m, "")
}

func (f *testFixtures) testVariadic(tb testing.TB) {
	parts := strings.Fields("a b c")
	joined := path.Join(parts...)
	assertEqual(tb, "a/b/c", joined, "path.Join over spread fields")
}

func (f *testFixtures) testIf(tb testing.TB) {
	req, err := http.NewRequest("GET", "https://example.com/api/users", nil)
	if err != nil {
		tb.Fatal(err)
	}
	ok := strings.HasPrefix(req.URL.Path, "/api")
	assertTrue(tb, ok, "")

	route := ""
	if ok {
		route = "api"
	} else {
		route = "static"
	}
	assertEqual(tb, "api", route, "")

	verdict := ""
	if req.Close {
		verdict = "close"
	} else if ok {
		verdict = "keep"
	} else {
		verdict = "drop"
	}
	assertEqual(tb, "keep", verdict, "")

	label := "none"
	if strings.HasPrefix(req.URL.Path, "/api") {
		if req.Close {
			label = "api-close"
		} else {
			label = "api-alive"
		}
	}
	assertEqual(tb, "api-alive", label, "")
}

func (f *testFixtures) testSince(tb testing.TB) {
	t := time.Now()
	req, err := http.NewRequest("GET", "https://example.com/api/users", nil)
	if err != nil {
		tb.Fatal(err)
	}

	status := 200
	class := ""
	if status == 200 {
		class = "ok"
	} else {
		class = "error"
	}
	assertEqual(tb, "ok", class, "")

	grade := ""
	if status >= 500 {
		grade = "5xx"
	} else if status >= 400 {
		grade = "4xx"
	} else {
		grade = "routine"
	}
	assertEqual(tb, "routine", grade, "")

	method := ""
	if req.Method == "GET" {
		method = "read"
	}
	assertEqual(tb, "read", method, "")

	kind := "load"
	if req.Method != "GET" {
		kind = "store"
	}
	assertEqual(tb, "load", kind, "")

	age := "stale"
	if time.Since(t) < time.Hour {
		age = "fresh"
	}
	assertEqual(tb, "fresh", age, "")
}

func (f *testFixtures) testFuncLit(tb testing.TB) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		fmt.Fprint(w, "ok")
	})
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assertEqual(tb, "201", fmt.Sprintf("%d", rec.Code), "")
	assertEqual(tb, "ok", rec.Body.String(), "")
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
