package gozero

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// benchResponseWriter is the cheapest ResponseWriter that satisfies
// fmt.Fprint: no recorder allocations, so the measured loop is the
// handler dispatch and the write, nothing else.
type benchResponseWriter struct {
	code int
	n    int
}

func (w *benchResponseWriter) Header() http.Header  { return nil }
func (w *benchResponseWriter) WriteHeader(code int) { w.code = code }
func (w *benchResponseWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	return len(p), nil
}

// BenchmarkFuncOf is the fixture comparison for the materialized
// handler: the same program FuncOf wraps for mux.HandleFunc against
// the identical native closure. The program is pinned to the direct
// tier, so the funcof side measures the reflect.MakeFunc bridge plus
// the program, and the native side the closure alone.
func BenchmarkFuncOf(b *testing.B) {
	const src = `fmt.Fprint(w, "ok");`
	rt := newBenchFixtureRuntime(b)
	if err := rt.Supports(src); err != nil {
		b.Fatalf("the handler program should JIT: %v", err)
	}
	handler, err := rt.FuncOf[http.HandlerFunc](src, "w", "r")
	if err != nil {
		b.Fatal(err)
	}
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})
	req := httptest.NewRequest("GET", "/health", nil)

	b.Run("handler/funcof", func(b *testing.B) {
		w := &benchResponseWriter{}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			handler(w, req)
		}
	})
	b.Run("handler/native", func(b *testing.B) {
		w := &benchResponseWriter{}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			native(w, req)
		}
	})
}
