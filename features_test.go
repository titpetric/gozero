package gozero

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strings"
	"testing"
)

func featRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime()
	for scope, fns := range map[string]map[string]any{
		"http":    {"NewRequest": http.NewRequest, "NewRequestWithContext": http.NewRequestWithContext},
		"url":     {"Parse": url.Parse},
		"json":    {"NewEncoder": json.NewEncoder},
		"context": {"Background": context.Background},
		"path":    {"Join": path.Join},
		"strings": {"Fields": strings.Fields},
	} {
		if err := rt.BindScope(scope, fns); err != nil {
			t.Fatal(err)
		}
	}
	return rt
}

// TestNoShadow pins the rule that a program name may not collide with a
// binding's namespace: url := ... with url.Parse bound would compile
// and then resolve the other way on every later use.
func TestNoShadow(t *testing.T) {
	rt := featRuntime(t)
	for _, src := range []string{
		`url := http.NewRequest("GET", "/");`,
		`http = 5;`,
		`var json int64;`,
		`var dest int64;`,
	} {
		_, err := rt.Compile(src)
		if err == nil || !strings.Contains(err.Error(), "shadows") && !strings.Contains(err.Error(), "dest") {
			t.Errorf("%s: err = %v, want a shadowing error", src, err)
		}
	}
	// A name that is not a binding prefix stays legal.
	if _, err := rt.Compile(`u := url.Parse("/ok"); return u;`); err != nil {
		t.Errorf("non-shadowing name: %v", err)
	}
}

type ctxKey struct{}

// TestContextAutoFill checks the plumbing rules: a context.Context
// parameter the program does not write receives the execution context,
// a context the program made and passed explicitly wins, and the plain
// Exec falls back to context.Background.
func TestContextAutoFill(t *testing.T) {
	rt := featRuntime(t)
	var got context.Context
	if err := rt.Bind("take", func(ctx context.Context, tag string) (*url.URL, error) {
		got = ctx
		return &url.URL{Path: tag}, nil
	}); err != nil {
		t.Fatal(err)
	}

	// Auto-filled: the source never mentions a context.
	fn, err := rt.Compile(`return take("auto");`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), ctxKey{}, "outer")
	u, err := fn.ExecContext[*url.URL](ctx, nil)
	if err != nil || u.Path != "auto" {
		t.Fatalf("got %v, %v", u, err)
	}
	if got.Value(ctxKey{}) != "outer" {
		t.Errorf("binding saw %v, want the execution context", got)
	}

	// Explicit: a program-bound context consumes the parameter.
	fn, err = rt.Compile(`own := context.Background(); return take(own, "explicit");`)
	if err != nil {
		t.Fatal(err)
	}
	u, err = fn.ExecContext[*url.URL](ctx, nil)
	if err != nil || u.Path != "explicit" {
		t.Fatalf("got %v, %v", u, err)
	}
	if got.Value(ctxKey{}) != nil {
		t.Error("the explicit context leaked the execution context's value")
	}

	// Exec without a context still runs, on Background.
	fn, err = rt.Compile(`return take("plain");`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[*url.URL](nil); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("binding saw a nil context")
	}

	// Auto-fill reaches http.NewRequestWithContext as advertised.
	fn, err = rt.Compile(`req := http.NewRequestWithContext("GET", "/c"); return req;`)
	if err != nil {
		t.Fatal(err)
	}
	req, err := fn.ExecContext[*http.Request](ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Context().Value(ctxKey{}) != "outer" {
		t.Error("the request did not carry the execution context")
	}
}

// TestFieldAssignment covers the write side of field access, on both
// tiers, with a literal and with a call result.
func TestFieldAssignment(t *testing.T) {
	rt := featRuntime(t)
	const src = `
		req := http.NewRequest("GET", "/");
		req.Method = "POST";
		json.NewEncoder(dest).Encode(req.Method);
	`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("a literal field store should JIT: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := fn.Scan(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "\"POST\"\n"; got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}

	// A call result on the right side.
	const call = `
		req := http.NewRequest("GET", "/old");
		req.URL = url.Parse("/new");
		json.NewEncoder(dest).Encode(req.URL.Path);
	`
	fn, err = rt.Compile(call)
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := fn.Scan(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "\"/new\"\n"; got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}

	// A var-declared struct is settable in place on the reflect tier.
	const value = `
		var u url.URL;
		u.Path = "/v";
		json.NewEncoder(dest).Encode(u.Path);
	`
	fn, err = rt.Compile(value)
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := fn.Scan(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "\"/v\"\n"; got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}

	// Errors: unknown field, unexported field, name on the right.
	for _, bad := range []string{
		`req := http.NewRequest("GET", "/"); req.Nope = "x";`,
		`req := http.NewRequest("GET", "/"); req.ctx = "x";`,
		`req := http.NewRequest("GET", "/"); s := req.Host; req.Host = s;`,
	} {
		if _, err := rt.Compile(bad); err == nil {
			t.Errorf("%s: expected a compile error", bad)
		}
	}
}

// TestVariadic covers packing individual arguments and spreading a
// slice, and the errors around a misplaced spread.
func TestVariadic(t *testing.T) {
	rt := featRuntime(t)
	// The tier is asserted for every legal form with the result named:
	// a silent fall back to the reflect evaluator would leave the pack
	// and spread nodes untested while the test still passed. A call
	// returned directly (return path.Join(...)) does not reach the JIT
	// at all, because a returned value needs a slot; that is a known
	// planner gap, not a variadic one.
	for _, src := range []string{
		`s := path.Join("a", "b", "c"); return s;`,
		`parts := strings.Fields("x y z"); s := path.Join(parts...); return s;`,
		`s := path.Join(); return s;`,
	} {
		if err := rt.Supports(src); err != nil {
			t.Errorf("%s: did not JIT: %v", src, err)
		}
	}

	fn, err := rt.Compile(`return path.Join("a", "b", "c");`)
	if err != nil {
		t.Fatal(err)
	}
	s, err := fn.Exec[string](nil)
	if err != nil || s != "a/b/c" {
		t.Fatalf("pack: got %q, %v", s, err)
	}

	fn, err = rt.Compile(`parts := strings.Fields("x y z"); return path.Join(parts...);`)
	if err != nil {
		t.Fatal(err)
	}
	s, err = fn.Exec[string](nil)
	if err != nil || s != "x/y/z" {
		t.Fatalf("spread: got %q, %v", s, err)
	}

	// Zero variadic arguments is a legal call.
	fn, err = rt.Compile(`return path.Join();`)
	if err != nil {
		t.Fatal(err)
	}
	if s, err := fn.Exec[string](nil); err != nil || s != "" {
		t.Fatalf("empty: got %q, %v", s, err)
	}

	for _, bad := range []string{
		`u := url.Parse("/a"); return path.Join(u...);`,
		`parts := strings.Fields("x"); return url.Parse(parts...);`,
		`parts := strings.Fields("x"); return path.Join(parts..., "y");`,
	} {
		if _, err := rt.Compile(bad); err == nil {
			t.Errorf("%s: expected a compile error", bad)
		}
	}
}

// recordingTB captures assertion failures so a fixture-style failing
// program can be shown to fail rather than pass silently.
type recordingTB struct {
	testing.TB
	failed bool
}

func (r *recordingTB) Errorf(string, ...any) { r.failed = true }

// TestAssertFailurePropagates checks the fixture mechanism itself: a
// failing assert.Equal must mark the TB the program was handed.
func TestAssertFailurePropagates(t *testing.T) {
	rt := fixtureRuntime(t)
	fn, err := rt.Compile(`assert.Equal(tb, "want", "got");`)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingTB{TB: t}
	if _, err := fn.Exec[any](map[string]any{"tb": rec}); err != nil {
		t.Fatal(err)
	}
	if !rec.failed {
		t.Fatal("a failing assertion did not reach the TB")
	}
}
