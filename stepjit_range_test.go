package gozero

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// TestRangeMatchesReflect runs every range form, every break and
// continue placement, and the two nestings with an if through both
// tiers, requiring identical results, effects and errors.
func TestRangeMatchesReflect(t *testing.T) {
	rt, seen := rangeRuntime(t)

	for name, src := range map[string]string{
		"slice value": `
			xs := fields("a b c");
			for _, s := range xs {
				rec(s);
			}
		`,
		"slice index": `
			xs := fields("a b c");
			for i := range xs {
				idx(i);
			}
		`,
		"index and value": `
			xs := fields("a b");
			for i, s := range xs {
				idx(i);
				rec(s);
			}
		`,
		"bare range": `
			xs := fields("a b");
			for range xs {
				poke();
			}
		`,
		"range over a call": `
			for _, s := range fields("x y") {
				rec(s);
			}
		`,
		"integer literal": `
			for i := range 3 {
				touch(i);
			}
		`,
		"integer slot": `
			n := count();
			for i := range n {
				idx(i);
			}
		`,
		"integer zero runs never": `
			n := zero();
			for i := range n {
				idx(i);
			}
			poke();
		`,
		"negative bound runs never": `
			n := neg();
			for i := range n {
				idx(i);
			}
			poke();
		`,
		"nested": `
			xs := fields("a b");
			for _, s := range xs {
				for i := range 2 {
					rec(s);
					touch(i);
				}
			}
		`,
		"empty body": `
			xs := fields("a b");
			for range xs {
			}
			poke();
		`,
		"body name outlives the loop": `
			xs := fields("a b c");
			for _, s := range xs {
				j := join("p", s);
			}
			rec(j);
		`,
		"body name of an empty loop reads zero": `
			ys := fields("");
			for _, s := range ys {
				j2 := join("q", s);
			}
			rec(j2);
		`,
		"loop variable after the loop": `
			for i := range 3 {
			}
			touch(i);
		`,
		"loop variable of an empty loop reads zero": `
			n := zero();
			for i := range n {
			}
			idx(i);
		`,
		"error stops mid-loop": `
			xs := fields("a b c");
			for _, s := range xs {
				rec(s);
				fail(s);
			}
		`,
		"loop then return": `
			xs := fields("p q");
			for _, s := range xs {
				rec(s);
			}
			return xs;
		`,
		"break leaves the rest": `
			xs := fields("a b c");
			for _, s := range xs {
				rec(s);
				break;
			}
			poke();
		`,
		"break on the first statement": `
			for i := range 5 {
				break;
				touch(i);
			}
			touch(i);
		`,
		"continue skips the tail": `
			for i := range 3 {
				touch(i);
				continue;
				poke();
			}
		`,
		"nested break exits the inner loop": `
			for i := range 2 {
				for j := range 5 {
					touch(j);
					break;
				}
				touch(i);
			}
		`,
		"nested continue stays inner": `
			for i := range 2 {
				for j := range 2 {
					continue;
					touch(j);
				}
				touch(i);
			}
		`,
		"error beats break": `
			for i := range 3 {
				fail("x");
				break;
			}
		`,
		"break inside an if arm": `
			xs := fields("a b c");
			for _, s := range xs {
				rec(s);
				if isA(s) {
					continue;
				}
				break;
			}
			poke();
		`,
		"a loop inside an if arm": `
			ok := yes();
			if ok {
				for i := range 2 {
					touch(i);
				}
			}
			touch(i);
		`,
	} {
		jit, slow := compilePair(t, rt, src)

		*seen = nil
		jitRes, jitErr := jit(context.Background(), nil, nil)
		jitSeen := fmt.Sprintf("%v", *seen)

		*seen = nil
		slowRes, slowErr := slow(context.Background(), nil, nil)
		slowSeen := fmt.Sprintf("%v", *seen)

		switch {
		case (jitErr == nil) != (slowErr == nil):
			t.Errorf("%s: err = %v (jit) vs %v (reflect)", name, jitErr, slowErr)
		case jitErr != nil && jitErr.Error() != slowErr.Error():
			t.Errorf("%s: err = %q (jit) vs %q (reflect)", name, jitErr, slowErr)
		}
		if jitSeen != slowSeen {
			t.Errorf("%s: effects %s (jit) vs %s (reflect)", name, jitSeen, slowSeen)
		}
		if fmt.Sprintf("%v", jitRes) != fmt.Sprintf("%v", slowRes) {
			t.Errorf("%s: result = %v (jit) vs %v (reflect)", name, jitRes, slowRes)
		}
	}
}

// TestRangeContextBoundsTheLoop pins the termination guarantee on
// both tiers: a cancelled execution context ends a spinning loop with
// ctx.Err() at the next iteration boundary, having run no iteration
// past the cancel.
func TestRangeContextBoundsTheLoop(t *testing.T) {
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
	const src = `
		for i := range 1000000 {
			spin();
		}
	`
	jit, slow := compilePair(t, rt, src)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		calls = 0
		_, err := fn(ctx, nil, nil)
		if err != context.Canceled {
			t.Errorf("%s: err = %v, want context.Canceled", name, err)
		}
		if calls != 3 {
			t.Errorf("%s: the loop ran %d iterations past the cancel", name, calls-3)
		}
		cancel()
	}
}

// TestRangeSupports pins what declines and what does not: a slice or
// integer range is a direct program, an element type with no layout
// class names its reason and still runs on the reflect evaluator.
func TestRangeSupports(t *testing.T) {
	rt, _ := rangeRuntime(t)
	if err := rt.Bind("vals", func() []url.URL {
		return []url.URL{{Path: "/v"}}
	}); err != nil {
		t.Fatal(err)
	}

	if err := rt.Supports(`for _, s := range fields("a b") { rec(s) }
`); err != nil {
		t.Errorf("a slice range should be direct: %v", err)
	}
	if err := rt.Supports(`for i := range 4 { touch(i) }
`); err != nil {
		t.Errorf("an integer range should be direct: %v", err)
	}

	// []url.URL has a struct element with no layout class: the JIT
	// declines with the reason, the reflect evaluator runs it.
	src := `
		us := vals();
		for _, u := range us {
			poke();
		}
	`
	err := rt.Supports(src)
	if err == nil {
		t.Fatal("a struct element should decline the direct tier")
	}
	if !strings.Contains(err.Error(), "layout class") {
		t.Errorf("the reason does not name the layout class: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.ExecContext[any](context.Background(), nil); err != nil {
		t.Errorf("the reflect evaluator should run it: %v", err)
	}
}

// BenchmarkRangeAliasing measures where the write-once aliasing rule
// turns off: the same four calls passing a string to an any
// parameter, once from a write-once slot that aliases the frame and
// once from a loop variable, which is written per iteration and
// boxes per call the way the Go compiler boxes at the same site.
func BenchmarkRangeAliasing(b *testing.B) {
	rt := NewRuntime()
	if err := rt.Bind("fields", strings.Fields); err != nil {
		b.Fatal(err)
	}
	if err := rt.Bind("one", func() string {
		return "a longer string that does not fit a static box"
	}); err != nil {
		b.Fatal(err)
	}
	if err := rt.Bind("sink", func(v any) *url.URL { return nil }); err != nil {
		b.Fatal(err)
	}
	for name, src := range map[string]string{
		"write-once": `s := one(); sink(s); sink(s); sink(s); sink(s);`,
		"loop": `
			xs := fields("wwww xxxx yyyy zzzz");
			for _, s := range xs {
				sink(s);
			}
		`,
	} {
		if err := rt.Supports(src); err != nil {
			b.Fatalf("%s: %v", name, err)
		}
		fn, err := rt.Compile(src)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			for b.Loop() {
				if _, err := fn(ctx, nil, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
