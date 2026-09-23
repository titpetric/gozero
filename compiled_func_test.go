package gozero

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
	// T not matching the result returns an error and no zero value.
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

	// A nil pointer result zeroes a value dest without faulting.
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

// assertEqual is the assertion surface the fixtures bind, without the
// testify dependency: reflect.DeepEqual and an Errorf. tb arrives as
// any because a fixture program passes it back through the stack, and
// message is a plain trailing parameter so the binding keeps a shape
// the direct-call tier can compile; an omitted message zero-fills.
func assertEqual(tb any, want, got any, message string) {
	t, ok := tb.(testing.TB)
	if !ok {
		panic(fmt.Sprintf("assert.Equal: tb is %T, want a testing.TB", tb))
	}
	t.Helper()
	// Two comparable values compare as interfaces without reflection,
	// which is every string and scalar the fixtures assert on; DeepEqual
	// is the fallback for the rest.
	if wt := reflect.TypeOf(want); wt != nil && wt.Comparable() && reflect.TypeOf(got) == wt {
		if want == got {
			return
		}
	} else if reflect.DeepEqual(want, got) {
		return
	}
	if message != "" {
		t.Errorf("%s: got %v (%T), want %v (%T)", message, got, got, want, want)
		return
	}
	t.Errorf("got %v (%T), want %v (%T)", got, got, want, want)
}

// assertTrue is assertEqual for a lone condition.
func assertTrue(tb any, value bool, message string) {
	t, ok := tb.(testing.TB)
	if !ok {
		panic(fmt.Sprintf("assert.True: tb is %T, want a testing.TB", tb))
	}
	t.Helper()
	if value {
		return
	}
	if message != "" {
		t.Errorf("%s: condition is false", message)
		return
	}
	t.Error("condition is false")
}

// copyBoxed is the binding contract's copy for a borrowed any of
// unknown type: a fresh cell, a typed move into it, and a new box.
// A plain reflect.ValueOf(v).Interface() is not a copy: for a
// non-addressable value it returns an interface sharing the same
// box.
func copyBoxed(v any) any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil
	}
	nv := reflect.New(rv.Type()).Elem()
	nv.Set(rv)
	return nv.Interface()
}

type fixtureCtxKey struct{}

// fixtureRuntime binds the standard library surface the fixtures use,
// plus the assert bindings. tb travels on the stack, so a fixture
// makes its own test assertions: assert.Equal(tb, want, got). It takes
// a testing.TB so the fixture tests and the fixture benchmarks share
// one binding set.
func fixtureRuntime(tb testing.TB) *Runtime {
	tb.Helper()
	rt := NewRuntime()
	for scope, fns := range map[string]map[string]any{
		"http": {
			"NewRequest":            http.NewRequest,
			"NewRequestWithContext": http.NewRequestWithContext,
		},
		"url": {
			"Parse":      url.Parse,
			"ParseQuery": url.ParseQuery,
		},
		"json":    {"NewEncoder": json.NewEncoder},
		"bytes":   {"NewBufferString": bytesNewBufferString},
		"fmt":     {"Sprintf": fmt.Sprintf, "Sprint": fmt.Sprint},
		"strings": {"Fields": strings.Fields},
		"path":    {"Join": path.Join},
		// Equal has no variadic tail, (tb, want, got, message): every
		// parameter has a shape, so an assertion is a direct call. The
		// message is optional the way every trailing argument is,
		// zero-filled to "".
		"assert": {
			"Equal": assertEqual,
			"True":  assertTrue,
		},
	} {
		if err := rt.BindScope(scope, fns); err != nil {
			tb.Fatal(err)
		}
	}
	// ctxValue proves the execution context reached a binding the
	// program never wrote a context into.
	if err := rt.Bind("ctxValue", func(ctx context.Context) string {
		v, _ := ctx.Value(fixtureCtxKey{}).(string)
		return v
	}); err != nil {
		tb.Fatal(err)
	}
	// The value bindings the vars fixture reads and writes. argv and
	// cursor are per-runtime, so the fixture's writes are visible to it
	// and to nothing else; time.Hour and io.EOF are the immutable
	// case, a constant and a sentinel that a program may read and not
	// replace.
	argv := []string{"prog", "-v"}
	cursor := 0
	for name, v := range map[string]any{
		"os.Args":   Mutable(&argv),
		"cursor":    Mutable(&cursor),
		"time.Hour": time.Hour,
		"io.EOF":    io.EOF,
		"frozen":    []string{"kept"},
	} {
		if err := rt.BindVar(name, v); err != nil {
			tb.Fatal(err)
		}
	}
	for name, fn := range map[string]any{
		"append":  Append,
		"first":   func(v []string) string { return v[0] },
		"second":  func(v []string) string { return v[1] },
		"argc":    func(v []string) int { return len(v) },
		"replace": func(p *[]string, s string) { *p = []string{s} },
		"bump":    func(p *int) { *p++ },
		"ptrTo":   func(n int) *int { return &n },
		"seconds": func(d time.Duration) int64 { return int64(d / time.Second) },
		"message": func(e error) string { return e.Error() },
	} {
		if err := rt.Bind(name, fn); err != nil {
			tb.Fatal(err)
		}
	}
	return rt
}

// TestFixtures runs every testdata/*.txt program as a subtest. A
// fixture asserts its own results through the tb the runner passes; this
// runner only compiles, executes, and reports the tier.
func TestFixtures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no fixtures in testdata/")
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".txt")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			rt := fixtureRuntime(t)
			fn, err := rt.Compile(string(src))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if err := rt.Supports(string(src)); err != nil {
				t.Logf("tier: reflect (%v)", err)
			} else {
				t.Log("tier: JIT")
			}
			// The subtest's own context, not Background: cancellation
			// and deadlines flow into the bindings.
			ctx := context.WithValue(t.Context(), fixtureCtxKey{}, "fixture")
			if _, err := fn.ExecContext[any](ctx, map[string]any{"tb": t}); err != nil {
				t.Fatalf("run: %v", err)
			}
		})
	}
}

// recordingTB captures assertion failures so a test can show that a
// fixture-style failing program fails and does not pass silently.
type recordingTB struct {
	testing.TB
	failed bool
}

func (r *recordingTB) Errorf(string, ...any) { r.failed = true }

// TestAssertFailurePropagates checks the fixture mechanism itself: a
// failing assert.Equal must mark the TB the program receives.
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
