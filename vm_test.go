package gozero

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func vmRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime()
	if err := rt.BindScope("http", map[string]any{
		"NewRequest": http.NewRequest,
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindScope("json", map[string]any{
		"NewEncoder": json.NewEncoder,
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindScope("url", map[string]any{
		"Parse": url.Parse,
	}); err != nil {
		t.Fatal(err)
	}
	return rt
}

// TestProgramWritesThroughDest is the target program: four statements,
// no error named anywhere, no return, and the output arrives through
// the pointer passed to Scan. Cookies and Encode are not bindings; the
// program reaches them through the result types of http.NewRequest and
// json.NewEncoder.
func TestProgramWritesThroughDest(t *testing.T) {
	rt := vmRuntime(t)
	const src = `
		req := http.NewRequest("GET", "/");
		cookies := req.Cookies();
		enc := json.NewEncoder(dest);
		enc.Encode(cookies);
	`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	var dest bytes.Buffer
	if err := fn.Scan(&dest, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := dest.String(), "[]\n"; got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}
}

// TestProgramErrorBubbles checks that a failing call stops the program
// and surfaces through Scan without the source naming an error.
func TestProgramErrorBubbles(t *testing.T) {
	rt := vmRuntime(t)
	const src = `
		req := http.NewRequest("bad method", "/");
		enc := json.NewEncoder(dest);
		enc.Encode(req);
	`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	var dest bytes.Buffer
	err = fn.Scan(&dest, nil)
	if err == nil {
		t.Fatal("expected the NewRequest error to bubble up")
	}
	if dest.Len() != 0 {
		t.Errorf("dest = %q, want nothing written after the error", dest.String())
	}
}

// TestProgramDiscardedResult checks a statement with no left-hand side:
// the program discards the request but still checks its error.
func TestProgramDiscardedResult(t *testing.T) {
	rt := vmRuntime(t)
	const src = `
		http.NewRequest("bad method", "/");
		enc := json.NewEncoder(dest);
		enc.Encode("reached");
	`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	var dest bytes.Buffer
	if err := fn.Scan(&dest, nil); err == nil {
		t.Fatal("expected the discarded call's error to bubble up")
	}

	const ok = `
		http.NewRequest("GET", "/");
		enc := json.NewEncoder(dest);
		enc.Encode("reached");
	`
	fn, err = rt.Compile(ok)
	if err != nil {
		t.Fatal(err)
	}
	dest.Reset()
	if err := fn.Scan(&dest, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := dest.String(), "\"reached\"\n"; got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}
}

// TestProgramValueResult checks that a returned value reaches Exec and
// that Scan copies it into dest.
func TestProgramValueResult(t *testing.T) {
	rt := vmRuntime(t)
	const src = `
		req := http.NewRequest("GET", "https://example.com/a/b");
		return url.Parse("https://example.com/a/b");
	`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	u, err := fn.Exec[*url.URL](nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/a/b" {
		t.Errorf("path = %q, want /a/b", u.Path)
	}

	var scanned url.URL
	if err := fn.Scan(&scanned, nil); err != nil {
		t.Fatal(err)
	}
	if scanned.Path != "/a/b" {
		t.Errorf("scanned path = %q, want /a/b", scanned.Path)
	}
}

// TestProgramDestOnlyFromScan checks that a program naming dest fails
// under Exec, which has no destination to pass it.
func TestProgramDestOnlyFromScan(t *testing.T) {
	rt := vmRuntime(t)
	fn, err := rt.Compile(`json.NewEncoder(dest).Encode("x");`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err == nil {
		t.Fatal("expected an error naming dest under Exec")
	}
}

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
