package gozero

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// BenchmarkFuncLit isolates the construct: the same handler body as
// BenchmarkFuncOf, but materialized from a func literal through the
// closure table rather than through reflect.MakeFunc. The program is
// pinned to the direct tier, so the funclit side measures the shape
// closure's dispatch plus the body, and the native side the identical
// Go closure.
func BenchmarkFuncLit(b *testing.B) {
	rt := newBenchFixtureRuntime(b)
	var handler http.HandlerFunc
	if err := rt.Bind("keep", func(h http.HandlerFunc) { handler = h }); err != nil {
		b.Fatal(err)
	}
	const src = `keep(func(w, r) { fmt.Fprint(w, "ok") });`
	if err := rt.Supports(src); err != nil {
		b.Fatalf("the literal should reach the closure table: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err != nil {
		b.Fatal(err)
	}
	native := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})
	req := httptest.NewRequest("GET", "/health", nil)

	b.Run("handler/funclit", func(b *testing.B) {
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
