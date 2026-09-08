package gozero

import (
	"net/http"
	"testing"
	"time"
)

var (
	sinkReq *http.Request
	sinkErr error
)

func benchRuntime(b *testing.B) *Runtime {
	b.Helper()
	rt := NewRuntime()
	if err := rt.Bind("NewRequest", http.NewRequest); err != nil {
		b.Fatal(err)
	}
	return rt
}

// reportVsNative runs after the measured loop, with the timer stopped:
// it times b.N iterations of the native baseline with its own stopwatch
// and reports it as native-ns/op, and the statement's cost over native
// as cost-ns/op. Both cost benchmarks carry their baseline inline, so
// the derived number survives on its own line of output.
func reportVsNative(b *testing.B) {
	b.Helper()
	vm := b.Elapsed()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		sinkReq, sinkErr = http.NewRequest("GET", "https://example.com/index.html", nil)
	}
	native := time.Since(start)
	b.ReportMetric(float64(native.Nanoseconds())/float64(b.N), "native-ns/op")
	b.ReportMetric(float64((vm-native).Nanoseconds())/float64(b.N), "cost-ns/op")
}

// BenchmarkNative is the baseline: the direct Go call the VM statement
// compiles down to.
func BenchmarkNative(b *testing.B) {
	for b.Loop() {
		sinkReq, sinkErr = http.NewRequest("GET", "https://example.com/index.html", nil)
	}
}

// BenchmarkCostWithoutCaching parses, compiles and executes the
// statement on every iteration, bypassing the expression cache: the
// first-run cost of a statement. cost-ns/op is (no-cache - native), the
// price of parsing and compilation plus the execution overhead.
func BenchmarkCostWithoutCaching(b *testing.B) {
	rt := benchRuntime(b)
	stack := map[string]any{"url": "https://example.com/index.html"}
	for b.Loop() {
		fn, err := rt.compileUncached(`return NewRequest("GET", url);`)
		if err != nil {
			b.Fatal(err)
		}
		sinkReq, sinkErr = fn.Exec[*http.Request](stack)
	}
	reportVsNative(b)
}

// BenchmarkCostNaive is BenchmarkCostAmortizedCache with the JIT tier
// bypassed: the same statement, compiled once, executed through
// Statement.call, the reflect fallback the compiler builds for
// bindings outside the shape table. It takes the same
// func(map[string]any) (any, error) shape and reads its variable off
// whatever stack it is handed, so the only difference between the two
// is reflect.Value.Call against the direct call. cost-ns/op is
// (naive - native).
func BenchmarkCostNaive(b *testing.B) {
	rt := benchRuntime(b)
	s, err := compileFlat(rt, `return NewRequest("GET", url);`)
	if err != nil {
		b.Fatal(err)
	}
	fn := CompiledFunc(s.call)
	stack := map[string]any{"url": "https://example.com/index.html"}
	for b.Loop() {
		sinkReq, sinkErr = fn.Exec[*http.Request](stack)
	}
	reportVsNative(b)
}

// BenchmarkCostAmortizedCache executes the once-compiled statement,
// reused verbatim against the stack: the cost of every consecutive run.
// cost-ns/op is (cached - native), the amortized overhead of going
// through the VM at all.
func BenchmarkCostAmortizedCache(b *testing.B) {
	rt := benchRuntime(b)
	fn, err := rt.Compile(`return NewRequest("GET", url);`)
	if err != nil {
		b.Fatal(err)
	}
	stack := map[string]any{"url": "https://example.com/index.html"}
	for b.Loop() {
		sinkReq, sinkErr = fn.Exec[*http.Request](stack)
	}
	reportVsNative(b)
}
