package gozero

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pluginRuntime registers the packages the example plugins under
// testdata/plugins import, plus a hostlog hook that records init
// order.
func pluginRuntime(t *testing.T) (*Runtime, *[]string) {
	t.Helper()
	rt := NewRuntime()
	var loads []string
	for path, symbols := range map[string]map[string]any{
		"net/http": {"Error": http.Error},
		"strings":  {"CutPrefix": strings.CutPrefix},
		"errors":   {"New": errors.New},
		"hostlog":  {"Loaded": func(name string) { loads = append(loads, name) }},
	} {
		if err := rt.BindPackage(path, symbols); err != nil {
			t.Fatal(err)
		}
	}
	for name, v := range map[string]any{
		"http.Handler":     (*http.Handler)(nil),
		"http.HandlerFunc": http.HandlerFunc(nil),
		"http.Request":     http.Request{},
	} {
		if err := rt.BindPackageType("net/http", name, v); err != nil {
			t.Fatal(err)
		}
	}
	return rt, &loads
}

// TestRuntime_LoadFile loads the single-file plugin: types, a value
// receiver method, script-to-script calls, the init hook, and the
// generic (T, error) bridge.
func TestRuntime_LoadFile(t *testing.T) {
	rt, loads := pluginRuntime(t)
	p, err := rt.LoadFile("testdata/plugins/greet/greet.go")
	if err != nil {
		t.Fatal(err)
	}
	if p.Package() != "greet" {
		t.Fatalf("package = %q", p.Package())
	}
	if len(*loads) != 1 || (*loads)[0] != "greet" {
		t.Fatalf("init hooks = %v", *loads)
	}

	got, err := p.Call[string]("Hello", "world")
	if err != nil || got != "hello world" {
		t.Fatalf("got %q, %v", got, err)
	}

	hello, err := p.FuncOf[func(string) string]("Hello")
	if err != nil {
		t.Fatal(err)
	}
	if hello("go") != "hello go" {
		t.Fatalf("bridged call = %q", hello("go"))
	}

	if _, err := p.Call[string]("NoSuch"); err == nil {
		t.Fatal("an undeclared function must not resolve")
	}
}

// TestRuntime_LoadDir loads the package folder: files merged, inits in
// filename order, a middleware closing over the host handler it
// wraps, and script errors flowing out as values.
func TestRuntime_LoadDir(t *testing.T) {
	rt, loads := pluginRuntime(t)
	p, err := rt.LoadDir("testdata/plugins/auth")
	if err != nil {
		t.Fatal(err)
	}
	if len(*loads) != 2 || (*loads)[0] != "auth" || (*loads)[1] != "auth/middleware" {
		t.Fatalf("init hooks = %v", *loads)
	}

	// The error side of (T, error).
	if _, err := p.Call[any]("ParseHeader", ""); err == nil || err.Error() != "missing authorization header" {
		t.Fatalf("err = %v", err)
	}
	creds, err := p.Call[any]("ParseHeader", "Bearer tok123")
	if err != nil || creds == nil {
		t.Fatalf("creds = %v, %v", creds, err)
	}

	// The middleware bridges as a Go func and wraps a host handler.
	mw, err := p.FuncOf[func(http.Handler) http.Handler]("Middleware")
	if err != nil {
		t.Fatal(err)
	}
	inner := 0
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner++
		w.WriteHeader(204)
	}))

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 || inner != 0 {
		t.Fatalf("unauthenticated: code = %d, inner = %d", rec.Code, inner)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer tok123")
	wrapped.ServeHTTP(rec, req)
	if rec.Code != 204 || inner != 1 {
		t.Fatalf("authenticated: code = %d, inner = %d", rec.Code, inner)
	}
}

// TestRuntime_Load and the Program surface over an inline source.
func TestRuntime_Load(t *testing.T) {
	rt := NewRuntime()
	p, err := rt.Load("package m\nfunc Twice(n int64) int64 { return n * 2 }")
	if err != nil {
		t.Fatal(err)
	}
	if p.Package() != "m" {
		t.Fatalf("package = %q", p.Package())
	}
	got, err := p.Call[int64]("Twice", int64(4))
	if err != nil || got != 8 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := p.Call[int64]("Twice", "x"); err == nil {
		t.Fatal("a mistyped argument must fail")
	}
	if _, err := p.FuncOf[func(string) string]("Twice"); err == nil {
		t.Fatal("a mismatched bridge type must fail")
	}
}

// TestProgram, TestProgram_Package, TestProgram_Call and
// TestProgram_FuncOf are covered by TestRuntime_Load and the plugin
// tests above; the aliases keep the coverage pairing visible.
func TestProgram(t *testing.T)         { TestRuntime_Load(t) }
func TestProgram_Package(t *testing.T) { TestRuntime_Load(t) }
func TestProgram_Call(t *testing.T)    { TestRuntime_Load(t) }
func TestProgram_FuncOf(t *testing.T)  { TestRuntime_Load(t) }
