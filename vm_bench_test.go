package gozero

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

var sinkBuf bytes.Buffer

const vmProgramSrc = `
	req := http.NewRequest("GET", "/");
	cookies := req.Cookies();
	enc := json.NewEncoder(dest);
	enc.Encode(cookies);
`

func benchVMRuntime(b *testing.B) *Runtime {
	b.Helper()
	rt := NewRuntime()
	if err := rt.BindScope("http", map[string]any{"NewRequest": http.NewRequest}); err != nil {
		b.Fatal(err)
	}
	if err := rt.BindScope("json", map[string]any{"NewEncoder": json.NewEncoder}); err != nil {
		b.Fatal(err)
	}
	return rt
}

// nativeProgram is the Go the VM program compiles down to.
func nativeProgram(dest *bytes.Buffer) error {
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		return err
	}
	cookies := req.Cookies()
	enc := json.NewEncoder(dest)
	return enc.Encode(cookies)
}

// reportVsNativeProgram is reportVsNative for the multi-statement
// program: same inline baseline, same derived cost-ns/op.
func reportVsNativeProgram(b *testing.B) {
	b.Helper()
	vm := b.Elapsed()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		sinkBuf.Reset()
		sinkErr = nativeProgram(&sinkBuf)
	}
	native := time.Since(start)
	b.ReportMetric(float64(native.Nanoseconds())/float64(b.N), "native-ns/op")
	b.ReportMetric(float64((vm-native).Nanoseconds())/float64(b.N), "cost-ns/op")
}

// BenchmarkProgramNative is the baseline for the four-statement
// program.
func BenchmarkProgramNative(b *testing.B) {
	for b.Loop() {
		sinkBuf.Reset()
		sinkErr = nativeProgram(&sinkBuf)
	}
}

// BenchmarkProgramCached executes the once-compiled program through
// Scan, reused verbatim: the cost of every consecutive run.
func BenchmarkProgramCached(b *testing.B) {
	rt := benchVMRuntime(b)
	fn, err := rt.Compile(vmProgramSrc)
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		sinkBuf.Reset()
		sinkErr = fn.Scan(&sinkBuf, nil)
	}
	reportVsNativeProgram(b)
}
