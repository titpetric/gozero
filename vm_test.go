package gozero

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
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
// no error is named anywhere, no return, and the output arrives through
// the pointer Scan was handed. Cookies and Encode are not bound; they
// are reached through the result types of http.NewRequest and
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
// the request is dropped but its error is still checked.
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
// under Exec, which has no destination to give it.
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
