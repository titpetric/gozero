package gozero

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime()
	if err := rt.Bind("NewRequest", http.NewRequest); err != nil {
		t.Fatal(err)
	}
	return rt
}

func TestRuntime_Eval(t *testing.T) {
	rt := newRuntime(t)
	req, err := rt.Eval[*http.Request](`return NewRequest("GET", "https://example.com/index.html");`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "GET" {
		t.Errorf("method = %q, want GET", req.Method)
	}
	if req.URL.Path != "/index.html" {
		t.Errorf("path = %q, want /index.html", req.URL.Path)
	}
	if req.Body != nil {
		t.Errorf("body = %v, want nil from zero-filled io.Reader", req.Body)
	}
}

func TestEvalSingleQuoted(t *testing.T) {
	rt := newRuntime(t)
	req, err := rt.Eval[*http.Request](`return NewRequest('GET', 'https://example.com/sq');`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.Path != "/sq" {
		t.Errorf("path = %q, want /sq", req.URL.Path)
	}
}

// TestEvalURLVariable feeds several links from a []string through the
// stack into the second parameter of NewRequest and checks the path of
// every returned and scanned value.
func TestEvalURLVariable(t *testing.T) {
	urls := []string{
		"https://example.com/one",
		"https://example.com/two/three",
		"https://example.com/",
		"https://example.com/a%20b",
	}

	rt := newRuntime(t)
	fn, err := rt.Compile(`return NewRequest("GET", url);`)
	if err != nil {
		t.Fatal(err)
	}

	stack := map[string]any{}
	for _, link := range urls {
		want, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		stack["url"] = link

		req, err := fn.Exec[*http.Request](stack)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		if req.URL.Path != want.Path {
			t.Errorf("%s: exec path = %q, want %q", link, req.URL.Path, want.Path)
		}

		var scanned http.Request
		if err := fn.Scan(&scanned, stack); err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		if scanned.URL.Path != want.Path {
			t.Errorf("%s: scan path = %q, want %q", link, scanned.URL.Path, want.Path)
		}
	}
}

func TestEvalUnsetVariable(t *testing.T) {
	rt := newRuntime(t)
	// url is not on the stack: it zero-fills to "" and NewRequest gets
	// an empty URL.
	req, err := rt.Eval[*http.Request](`return NewRequest("GET", url);`, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "" {
		t.Errorf("url = %q, want empty from zero-filled variable", req.URL)
	}
}

func TestEvalBodyVariable(t *testing.T) {
	rt := newRuntime(t)
	req, err := rt.Eval[*http.Request](`return NewRequest("POST", "https://example.com/post", body);`, map[string]any{
		"body": strings.NewReader("payload"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.Body == nil {
		t.Fatal("body = nil, want reader")
	}
}

// TestRuntime_Compile pins the cache: the second Compile of the same
// source is the same func.
func TestRuntime_Compile(t *testing.T) {
	rt := newRuntime(t)
	stmt := `return NewRequest("GET", "https://example.com");`
	a, err := rt.Compile(stmt)
	if err != nil {
		t.Fatal(err)
	}
	b, err := rt.Compile(stmt)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.ValueOf(a).Pointer() != reflect.ValueOf(b).Pointer() {
		t.Error("second Compile did not return the cached func")
	}
}

func TestCompileErrors(t *testing.T) {
	rt := newRuntime(t)
	for name, stmt := range map[string]string{
		"unknown binding": `return Missing("GET");`,
		"too many args":   `return NewRequest("GET", "https://example.com", body, "extra");`,
		"int literal":     `return NewRequest(42, "https://example.com");`,
		"float literal":   `return NewRequest(4.2, "https://example.com");`,
		"same-line extra": `return NewRequest("GET") extra`,
		"trailing input":  `return NewRequest("GET"); extra`,
		"unterminated":    `return NewRequest("GET`,
	} {
		if _, err := rt.Compile(stmt); err == nil {
			t.Errorf("%s: expected compile error for %q", name, stmt)
		}
	}
}

func TestExecVariableTypeMismatch(t *testing.T) {
	rt := newRuntime(t)
	fn, err := rt.Compile(`return NewRequest("GET", url);`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[*http.Request](map[string]any{"url": 42}); err == nil {
		t.Fatal("expected type mismatch for int stack value in string slot")
	}
}

func TestRuntime_Bind(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("x", 42); err == nil {
		t.Fatal("expected error binding a non-func")
	}
	if err := rt.Bind("f", strings.ToUpper); err != nil {
		t.Fatal(err)
	}
	got, err := rt.Eval[string](`return f("abc");`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ABC" {
		t.Errorf("got %q, want ABC", got)
	}
}

// TestRuntime_BindValue pins the value-binding surface: a scalar, a
// string and a named-type value each read as a call argument with
// the static type they were bound with, on both compile paths. A
// func and a nil are rejected, the bound root cannot be shadowed,
// and a rebind overwrites, the rules Bind has.
func TestRuntime_BindValue(t *testing.T) {
	rt := NewRuntime()
	if err := rt.BindValue("limits.Max", func() {}); err == nil {
		t.Fatal("expected an error binding a func as a value")
	}
	if err := rt.BindValue("limits.Max", nil); err == nil {
		t.Fatal("expected an error binding nil")
	}
	for name, v := range map[string]any{
		"limits.Max": int64(10),
		"urls.Home":  "https://example.com/home",
		"time.Hour":  time.Hour,
	} {
		if err := rt.BindValue(name, v); err != nil {
			t.Fatal(err)
		}
	}
	for name, fn := range map[string]any{
		"itoa": func(v int64) string { return fmt.Sprint(v) },
		"path": func(u string) string { return strings.TrimPrefix(u, "https://example.com") },
		"span": func(d time.Duration) string { return d.String() },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	// One flat call compiles through compileStatement; a program with
	// a declaration compiles every argument through compileArg. The
	// named-type case only passes because the value keeps its static
	// type: span takes time.Duration, not int64.
	for src, want := range map[string]string{
		`return itoa(limits.Max);`:         "10",
		`return path(urls.Home);`:          "/home",
		`return span(time.Hour);`:          "1h0m0s",
		`s := itoa(limits.Max); return s;`: "10",
		`s := path(urls.Home); return s;`:  "/home",
		`s := span(time.Hour); return s;`:  "1h0m0s",
	} {
		got, err := rt.Eval[string](src, nil)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", src, got, want)
		}
	}
	// The static type flows into the assignability check: a
	// time.Duration does not pass as int64 on either path.
	for _, src := range []string{
		`return itoa(time.Hour);`,
		`s := itoa(time.Hour); return s;`,
	} {
		if _, err := rt.Compile(src); err == nil || !strings.Contains(err.Error(), "cannot use time.Duration as int64") {
			t.Errorf("%s: err = %v, want the named type rejected as int64", src, err)
		}
	}
	// The same binding reads as a comparison operand, which is the
	// position time.Hour exists for.
	got, err := rt.Eval[string](`s := "low"; if 3 < limits.Max { s = itoa(limits.Max); }; return s;`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "10" {
		t.Errorf("comparison got %q, want 10", got)
	}
	if _, err := rt.Eval[string](`limits := "x"; return limits;`, nil); err == nil {
		t.Error("expected the value root to reject shadowing")
	}
	if err := rt.BindValue("limits.Max", int64(12)); err != nil {
		t.Fatal(err)
	}
	got, err = rt.Eval[string](`v := itoa(limits.Max); return v;`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "12" {
		t.Errorf("rebind got %q, want 12", got)
	}
}

// TestRuntime is the round trip in one place: construct, bind, compile,
// exec, scan.
func TestRuntime(t *testing.T) {
	rt := newRuntime(t)
	fn, err := rt.Compile(`return NewRequest("GET", "https://example.com/rt");`)
	if err != nil {
		t.Fatal(err)
	}
	req, err := fn.Exec[*http.Request](nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.Path != "/rt" {
		t.Errorf("exec path = %q, want /rt", req.URL.Path)
	}
	var scanned http.Request
	if err := fn.Scan(&scanned, nil); err != nil {
		t.Fatal(err)
	}
	if scanned.URL.Path != "/rt" {
		t.Errorf("scan path = %q, want /rt", scanned.URL.Path)
	}
}

func TestNewRuntime(t *testing.T) {
	rt := NewRuntime()
	// The registry starts with the predeclared names, before any Bind.
	found := false
	for _, name := range rt.Types() {
		if name == "string" {
			found = true
		}
	}
	if !found {
		t.Error("string is not in a fresh runtime's type registry")
	}
	if _, err := rt.Compile(`return Missing();`); err == nil {
		t.Error("a fresh runtime should know no bindings")
	}
}

func TestRuntime_SetLogger(t *testing.T) {
	rt := NewRuntime()
	var buf bytes.Buffer
	rt.SetLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err := rt.Bind("NewRequest", http.NewRequest); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "bind") {
		t.Error("Bind logged nothing through the attached logger")
	}
}

func TestRuntime_BindScope(t *testing.T) {
	rt := NewRuntime()
	if err := rt.BindScope("strings", map[string]any{"Upper": strings.ToUpper}); err != nil {
		t.Fatal(err)
	}
	got, err := rt.Eval[string](`return strings.Upper("abc");`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ABC" {
		t.Errorf("got %q, want ABC", got)
	}
	if err := rt.BindScope("bad", map[string]any{"NotAFunc": 42}); err == nil {
		t.Error("expected the non-func entry to fail the scope")
	}
}

func TestRuntime_EvalContext(t *testing.T) {
	rt := newRuntime(t)
	var got context.Context
	if err := rt.Bind("take", func(ctx context.Context, tag string) (*url.URL, error) {
		got = ctx
		return &url.URL{Path: tag}, nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), ctxKey{}, "outer")
	u, err := rt.EvalContext[*url.URL](ctx, `return take("tag");`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "tag" {
		t.Errorf("path = %q, want tag", u.Path)
	}
	if got.Value(ctxKey{}) != "outer" {
		t.Error("the binding did not receive the execution context")
	}
}

func TestRuntime_Supports(t *testing.T) {
	rt := newRuntime(t)
	if err := rt.Supports(`return NewRequest("GET", "https://example.com");`); err != nil {
		t.Errorf("NewRequest should reach the direct-call tier: %v", err)
	}
	// fmt.Stringer is not in ifaceConvs, so this signature cannot JIT.
	if err := rt.Bind("Fprint", func(s fmt.Stringer) (*http.Request, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Supports(`return Fprint(v);`); err == nil {
		t.Error("an interface outside ifaceConvs should not report as supported")
	}
	if err := rt.Supports(`not a program`); err == nil {
		t.Error("a parse error should come back from Supports")
	}
}
