package gozero

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureCtxKey struct{}

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
	return rt
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
