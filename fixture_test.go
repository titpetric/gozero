package gozero

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixtureCtxKey struct{}

// fixtureCounter and fixtureCfg are the receivers the vars fixture
// calls: a pointer-receiver method on a bound value, and a func-typed
// field called through.
type fixtureCounter struct{ N int }

func (c *fixtureCounter) Bump() { c.N++ }

type fixtureCfg struct{ Fn func() string }

// fixtureRuntime binds the standard library surface the fixtures use,
// plus the assert bindings. tb travels on the stack, so a fixture
// makes its own test assertions: assert.Equal(tb, want, got).
func fixtureRuntime(t *testing.T) *Runtime {
	t.Helper()
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
		"bytes":   {"NewBufferString": bytes.NewBufferString},
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
			t.Fatal(err)
		}
	}
	// ctxValue proves the execution context reached a binding the
	// program never wrote a context into.
	if err := rt.Bind("ctxValue", func(ctx context.Context) string {
		v, _ := ctx.Value(fixtureCtxKey{}).(string)
		return v
	}); err != nil {
		t.Fatal(err)
	}
	// chanOf is the channel source for the channels fixture: buffered
	// with headroom, so the fixture's sends never block.
	if err := rt.Bind("chanOf", chanOf); err != nil {
		t.Fatal(err)
	}
	// The values the vars fixture reads and writes. argv and cursor
	// are bound by address, so the fixture writes them through "*name
	// = v" and reads the writes back; they are per-runtime, so the
	// writes are visible to this subtest and nothing else. time.Hour,
	// io.EOF and frozen are bound by value: a program reads them and
	// writes its own copy for the run.
	argv := []string{"prog", "-v"}
	cursor := 0
	for name, v := range map[string]any{
		"os.Args":   &argv,
		"cursor":    &cursor,
		"time.Hour": time.Hour,
		"io.EOF":    io.EOF,
		"frozen":    []string{"kept"},
		"counter":   fixtureCounter{N: 1},
		"cfg":       fixtureCfg{Fn: func() string { return "from a field" }},
		"append":    Append,
		"first":     func(v []string) string { return v[0] },
		"second":    func(v []string) string { return v[1] },
		"third":     func(v []string) string { return v[2] },
		"argc":      func(v []string) int { return len(v) },
		"bump":      func(p *int) { *p++ },
		"ptrTo":     func(n int) *int { return &n },
		"seconds":   func(d time.Duration) int64 { return int64(d / time.Second) },
		"message":   func(e error) string { return e.Error() },
	} {
		if err := rt.Bind(name, v); err != nil {
			t.Fatal(err)
		}
	}
	return rt
}

// chanOf builds the buffered channel the channels fixture receives
// from and sends into.
func chanOf(vs ...string) chan string {
	c := make(chan string, len(vs)+2)
	for _, v := range vs {
		c <- v
	}
	return c
}

// TestFixtures runs every testdata/*.txt program as a subtest. A
// fixture asserts its own results through the tb it is handed; this
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
