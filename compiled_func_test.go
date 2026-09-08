package gozero

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestCompiledFunc calls the closure directly, the way Exec and Scan
// do underneath: the result comes back boxed in an any.
func TestCompiledFunc(t *testing.T) {
	rt := newRuntime(t)
	fn, err := rt.Compile(`return NewRequest("GET", "https://example.com/raw");`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := fn(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req, ok := res.(*http.Request)
	if !ok {
		t.Fatalf("result is %T, want *http.Request", res)
	}
	if req.URL.Path != "/raw" {
		t.Errorf("path = %q, want /raw", req.URL.Path)
	}
}

func TestCompiledFunc_Exec(t *testing.T) {
	rt := newRuntime(t)
	fn, err := rt.Compile(`return NewRequest("GET", "https://example.com/exec");`)
	if err != nil {
		t.Fatal(err)
	}
	req, err := fn.Exec[*http.Request](nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.Path != "/exec" {
		t.Errorf("path = %q, want /exec", req.URL.Path)
	}
	// T not matching the result is an error, not a zero value.
	if _, err := fn.Exec[string](nil); err == nil {
		t.Error("expected a type mismatch asking for a string")
	}
}

func TestCompiledFunc_ExecContext(t *testing.T) {
	rt := newRuntime(t)
	fn, err := rt.Compile(`return NewRequest("GET", "https://example.com/ctx");`)
	if err != nil {
		t.Fatal(err)
	}
	req, err := fn.ExecContext[*http.Request](context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.Path != "/ctx" {
		t.Errorf("path = %q, want /ctx", req.URL.Path)
	}
}

func TestCompiledFunc_Scan(t *testing.T) {
	rt := newRuntime(t)
	fn, err := rt.Compile(`return NewRequest("GET", "https://example.com/scan");`)
	if err != nil {
		t.Fatal(err)
	}
	// The *http.Request result dereferences into a caller-allocated
	// value.
	var req http.Request
	if err := fn.Scan(&req, nil); err != nil {
		t.Fatal(err)
	}
	if req.URL.Path != "/scan" {
		t.Errorf("path = %q, want /scan", req.URL.Path)
	}
	if err := fn.Scan[http.Request](nil, nil); err == nil {
		t.Error("expected an error for a nil dest")
	}
}

func TestCompiledFunc_ScanContext(t *testing.T) {
	rt := newRuntime(t)
	fn, err := rt.Compile(`return NewRequest("GET", "https://example.com/scanctx");`)
	if err != nil {
		t.Fatal(err)
	}
	var req http.Request
	if err := fn.ScanContext(context.Background(), &req, nil); err != nil {
		t.Fatal(err)
	}
	if req.URL.Path != "/scanctx" {
		t.Errorf("path = %q, want /scanctx", req.URL.Path)
	}
	if err := fn.ScanContext[http.Request](context.Background(), nil, nil); err == nil {
		t.Error("expected an error for a nil dest")
	}
}

// TestCompiledFunc_ScanBranches covers the three scanInto outcomes the
// deref path left cold: a result directly assignable to dest, a nil
// pointer result zeroing dest, and a result no dest type can take.
func TestCompiledFunc_ScanBranches(t *testing.T) {
	rt := newRuntime(t)
	if err := rt.Bind("nilReq", func() (*http.Request, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("word", func() string { return "w" }); err != nil {
		t.Fatal(err)
	}

	// Assignable: a *http.Request result into a *http.Request dest.
	fn, err := rt.Compile(`return NewRequest("GET", "https://example.com/assign");`)
	if err != nil {
		t.Fatal(err)
	}
	var ptr *http.Request
	if err := fn.Scan(&ptr, nil); err != nil {
		t.Fatal(err)
	}
	if ptr == nil || ptr.URL.Path != "/assign" {
		t.Errorf("scanned %v, want the request pointer", ptr)
	}

	// A nil pointer result zeroes a value dest rather than faulting.
	fn, err = rt.Compile(`r := nilReq(); return r`)
	if err != nil {
		t.Fatal(err)
	}
	junk := http.Request{Method: "JUNK"}
	if err := fn.Scan(&junk, nil); err != nil {
		t.Fatal(err)
	}
	if junk.Method != "" {
		t.Errorf("dest.Method = %q, want the zero value", junk.Method)
	}

	// A result the dest cannot hold is an error naming both types.
	fn, err = rt.Compile(`s := word(); return s`)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	err = fn.Scan(&n, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot scan") {
		t.Fatalf("err = %v, want the scan mismatch reported", err)
	}
}
