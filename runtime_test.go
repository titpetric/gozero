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
