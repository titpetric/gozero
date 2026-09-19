package gozero

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"
)

// loopRuntime is rangeRuntime plus the sources the L2 forms iterate:
// maps, channels, and iterator funcs of both shapes.
func loopRuntime(t testing.TB) (*Runtime, *[]string) {
	t.Helper()
	rt, seen := rangeRuntime(t)
	bind := map[string]any{
		// ch records a rune, the string range's value type.
		"ch": func(r rune) error {
			*seen = append(*seen, fmt.Sprintf("ch:%c", r))
			return nil
		},
		"single": func() map[string]string { return map[string]string{"k": "v"} },
		"sized":  func() map[string]int64 { return map[string]int64{"a": 1, "bb": 2, "ccc": 3} },
		"nilmap": func() map[string]int64 { return nil },
		// chOf hands back a buffered channel already closed: what a
		// channel range drains to its end.
		"chOf": func(vs ...string) chan string {
			c := make(chan string, len(vs))
			for _, v := range vs {
				c <- v
			}
			close(c)
			return c
		},
		"lines": strings.Lines,
		"pairs": slices.All[[]string],
		// rogue ignores its yield's answer: the misbehaving iterator
		// both tiers must stop with a panic, not a stale write.
		"rogue": func() func(func(string) bool) {
			return func(yield func(string) bool) {
				yield("a")
				yield("b")
			}
		},
	}
	for name, fn := range bind {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	return rt, seen
}

// assertLoopPair runs src down both tiers and requires identical
// results, effects and errors. sorted compares the effects as a
// multiset, for a source whose iteration order is its own per run.
func assertLoopPair(t *testing.T, rt *Runtime, seen *[]string, name, src string, sorted bool) {
	t.Helper()
	jit, slow := compilePair(t, rt, src)

	*seen = nil
	jitRes, jitErr := jit(context.Background(), nil, nil)
	jitSeen := append([]string(nil), *seen...)

	*seen = nil
	slowRes, slowErr := slow(context.Background(), nil, nil)
	slowSeen := append([]string(nil), *seen...)

	if sorted {
		sort.Strings(jitSeen)
		sort.Strings(slowSeen)
	}
	switch {
	case (jitErr == nil) != (slowErr == nil):
		t.Errorf("%s: err = %v (jit) vs %v (reflect)", name, jitErr, slowErr)
	case jitErr != nil && jitErr.Error() != slowErr.Error():
		t.Errorf("%s: err = %q (jit) vs %q (reflect)", name, jitErr, slowErr)
	}
	if fmt.Sprintf("%v", jitSeen) != fmt.Sprintf("%v", slowSeen) {
		t.Errorf("%s: effects %v (jit) vs %v (reflect)", name, jitSeen, slowSeen)
	}
	if fmt.Sprintf("%v", jitRes) != fmt.Sprintf("%v", slowRes) {
		t.Errorf("%s: result = %v (jit) vs %v (reflect)", name, jitRes, slowRes)
	}
}

// TestLoopsMatchReflect runs the string, map and channel range forms
// and every break and continue placement through both tiers. A map's
// iteration order is its own per run, so the cases that iterate one
// compare sorted.
func TestLoopsMatchReflect(t *testing.T) {
	rt, seen := loopRuntime(t)

	for name, tc := range map[string]struct {
		src    string
		sorted bool
	}{
		"string by rune": {src: `
			for i, r := range "aµb" {
				idx(i);
				ch(r);
			}
		`},
		"string key only": {src: `
			for i := range "abc" {
				idx(i);
			}
		`},
		"string bare": {src: `
			for range "ab" {
				poke();
			}
		`},
		"string empty": {src: `
			for i, r := range "" {
				idx(i);
				ch(r);
			}
			poke();
		`},
		"string from a name": {src: `
			s := join("a", "b");
			for i, r := range s {
				idx(i);
				ch(r);
			}
		`},
		"map single entry": {src: `
			m := single();
			for k, v := range m {
				rec(k);
				rec(v);
			}
		`},
		"map keys": {src: `
			m := sized();
			for k := range m {
				rec(k);
			}
		`, sorted: true},
		"map key and value": {src: `
			m := sized();
			for k, v := range m {
				rec(k);
				touch(v);
			}
		`, sorted: true},
		"map value only": {src: `
			m := sized();
			for _, v := range m {
				touch(v);
			}
		`, sorted: true},
		"map bare": {src: `
			m := sized();
			for range m {
				poke();
			}
		`},
		"map nil": {src: `
			m := nilmap();
			for k := range m {
				rec(k);
			}
			poke();
		`},
		"map break counts one": {src: `
			m := sized();
			for range m {
				poke();
				break;
			}
		`},
		"channel drained": {src: `
			c := chOf("a", "b", "c");
			for s := range c {
				rec(s);
			}
			poke();
		`},
		"channel bare": {src: `
			c := chOf("x");
			for range c {
				poke();
			}
		`},
		"channel break leaves the rest": {src: `
			c := chOf("p", "q");
			for s := range c {
				rec(s);
				break;
			}
			r := <-c;
			rec(r);
		`},
		"break in a slice range": {src: `
			xs := fields("a b c");
			for _, s := range xs {
				rec(s);
				break;
			}
			poke();
		`},
		"break on the first statement": {src: `
			for i := range 5 {
				break;
				touch(i);
			}
			touch(i);
		`},
		"continue skips the tail": {src: `
			for i := range 3 {
				touch(i);
				continue;
				poke();
			}
		`},
		"nested break exits the inner loop": {src: `
			for i := range 2 {
				for j := range 5 {
					touch(j);
					break;
				}
				touch(i);
			}
		`},
		"nested continue stays inner": {src: `
			for i := range 2 {
				for j := range 2 {
					continue;
					touch(j);
				}
				touch(i);
			}
		`},
		"break in a string range": {src: `
			for i, r := range "abc" {
				idx(i);
				ch(r);
				break;
			}
		`},
		"error beats break": {src: `
			for i := range 3 {
				fail("x");
				break;
			}
		`},
	} {
		assertLoopPair(t, rt, seen, name, tc.src, tc.sorted)
	}
}

// TestLoopSignalsCannotEscape pins that break and continue are
// rejected outside a loop body, on the parser's say-so, which is
// what keeps the loop signals from ever reaching a caller.
func TestLoopSignalsCannotEscape(t *testing.T) {
	rt, _ := loopRuntime(t)
	for name, src := range map[string]string{
		"break at top level":    `poke(); break`,
		"continue at top level": `continue; poke()`,
		"break after a loop":    `for i := range 2 { touch(i) }; break`,
	} {
		if _, err := rt.Compile(src); err == nil {
			t.Errorf("%s: compiled, want a parse error", name)
		} else if !strings.Contains(err.Error(), "only allowed inside a loop body") {
			t.Errorf("%s: err = %q, want the loop-body rule", name, err)
		}
	}
}

// TestLoopContextBounds pins the termination guarantee for the L2
// forms on both tiers: a run whose context dies stops at the next
// iteration boundary, whether the loop is spinning through an
// iterator or blocked on a channel that never closes.
func TestLoopContextBounds(t *testing.T) {
	rt := NewRuntime()
	calls := 0
	var cancel context.CancelFunc
	if err := rt.Bind("spin", func() *url.URL {
		calls++
		if calls == 3 {
			cancel()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// forever yields until its consumer stops asking; only the
	// context ends the loop below.
	if err := rt.Bind("forever", func() func(func(int64) bool) {
		return func(yield func(int64) bool) {
			for i := int64(0); ; i++ {
				if !yield(i) {
					return
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	// open holds one value and never closes, so the second receive
	// blocks until the armed select sees the context.
	if err := rt.Bind("open", func() chan string {
		c := make(chan string, 1)
		c <- "only"
		return c
	}); err != nil {
		t.Fatal(err)
	}

	for name, src := range map[string]string{
		"iterator spins": `
			for i := range forever() {
				spin();
			}
		`,
		"channel never closes": `
			c := open();
			for s := range c {
				spin(); spin(); spin();
			}
		`,
	} {
		jit, slow := compilePair(t, rt, src)
		for tier, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
			var ctx context.Context
			ctx, cancel = context.WithCancel(context.Background())
			calls = 0
			_, err := fn(ctx, nil, nil)
			if err != context.Canceled {
				t.Errorf("%s/%s: err = %v, want context.Canceled", name, tier, err)
			}
			if calls != 3 {
				t.Errorf("%s/%s: %d binding calls ran, want 3", name, tier, calls)
			}
			cancel()
		}
	}
}

// TestLoopSupports pins the tier of the string, map and channel forms
// and of a body carrying break and continue: all direct programs.
func TestLoopSupports(t *testing.T) {
	rt, _ := loopRuntime(t)
	for name, src := range map[string]string{
		"string":   `for i, r := range "ab" { idx(i); ch(r) }`,
		"map":      `m := sized(); for k, v := range m { rec(k); touch(v) }`,
		"channel":  `c := chOf("x"); for s := range c { rec(s) }`,
		"breaking": `for i := range 3 { touch(i); break }`,
		"skipping": `for i := range 3 { continue; touch(i) }`,
	} {
		if err := rt.Supports(src); err != nil {
			t.Errorf("%s should be a direct program: %v", name, err)
		}
	}
}
