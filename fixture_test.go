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
	"slices"
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
		"strings": {"Fields": strings.Fields, "Lines": strings.Lines},
		"slices":  {"All": slices.All[[]string]},
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
	// counter hands the range fixture a fresh accumulator per run.
	if err := rt.Bind("counter", counterNew); err != nil {
		t.Fatal(err)
	}
	// The loops fixture's sources: a fresh map per run, a closed
	// channel per range, and a length the language cannot take itself.
	// queue is the for fixture's condition source: a fresh drainable
	// queue per run.
	for name, fn := range map[string]any{
		"sizes":        sizesMap,
		"strlen":       strLen,
		"closedChanOf": closedChanOf,
		"queue":        queueOf,
	} {
		if err := rt.Bind(name, fn); err != nil {
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

// closedChanOf builds a buffered channel already holding its values
// and closed: the source a channel range drains to its end.
func closedChanOf(vs ...string) chan string {
	c := make(chan string, len(vs))
	for _, v := range vs {
		c <- v
	}
	close(c)
	return c
}

// sizesMap hands the loops fixture a fresh map per run, so iterations
// of the benchmark do not share one.
func sizesMap() map[string]int64 {
	return map[string]int64{"alpha": 1, "beta": 2, "gamma": 3}
}

// strLen measures a string where the language has no len, so a map
// key contributes an order-independent fact to an assertion.
func strLen(s string) int64 { return int64(len(s)) }

// fixtureQueue is the condition source of the for fixture: More
// reports whether an element remains and Next pops one, so a
// condition loop drains it in order and terminates.
type fixtureQueue struct{ items []string }

// More reports whether Next has an element to pop.
func (q *fixtureQueue) More() bool { return len(q.items) > 0 }

// Next pops the front element.
func (q *fixtureQueue) Next() string {
	v := q.items[0]
	q.items = q.items[1:]
	return v
}

// queueOf builds a fresh queue per run. The variadic pack is borrowed
// memory, so the slice is copied.
func queueOf(vs ...string) *fixtureQueue {
	return &fixtureQueue{items: append([]string(nil), vs...)}
}

// fixtureCounter is the accumulator the range fixture drives. A run
// creates its own through the counter binding, so benchmark
// iterations do not share state.
type fixtureCounter struct{ n int64 }

// Add returns a nil error so the method has a shape the direct tier
// calls.
func (c *fixtureCounter) Add(d int64) error { c.n += d; return nil }

// Sum reports the accumulated total.
func (c *fixtureCounter) Sum() int64 { return c.n }

// counterNew is the counter binding of the range fixture.
func counterNew() *fixtureCounter { return &fixtureCounter{} }

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
