package gozero

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// TestForMatchesReflect runs the condition and three-clause forms
// down both tiers and requires identical results, effects and errors:
// every operator, both post directions, the wrap at a fixed width,
// break and continue placement, and the flat-scope reads after the
// loop.
func TestForMatchesReflect(t *testing.T) {
	rt, seen := forRuntime(t)

	for name, src := range map[string]string{
		"count up": `
			for i := 0; i < 3; i++ {
				touch(i);
			}
		`,
		"count down": `
			for i := 3; i > 0; i-- {
				touch(i);
			}
		`,
		"not equal": `
			for i := bump(1); i != 6; i++ {
				touch(i);
			}
		`,
		"lte and gte": `
			for i := 0; i <= 2; i++ {
				touch(i);
			}
			for j := 2; j >= 0; j-- {
				touch(j);
			}
		`,
		"eq once": `
			for i := 0; i == 0; i++ {
				touch(i);
			}
		`,
		"zero iterations": `
			for i := 5; i < 3; i++ {
				touch(i);
			}
			poke();
		`,
		"bound from a name": `
			n := count();
			for i := 0; i < n; i++ {
				touch(i);
			}
		`,
		"bound from a call": `
			for i := 0; i < count(); i++ {
				touch(i);
			}
		`,
		"cond method": `
			b := budget(2);
			for b.More() {
				poke();
			}
		`,
		"cond name rewritten": `
			b := budget(2);
			ok := b.More();
			for ok {
				poke();
				ok = b.More();
			}
		`,
		"cond field": `
			g := gate(2);
			for g.Open {
				poke();
				g.Tick();
			}
		`,
		"cond zero iterations": `
			g := gate(0);
			for g.Open {
				poke();
			}
			poke();
		`,
		"break skips the post": `
			for i := 0; i < 5; i++ {
				poke();
				break;
			}
			touch(i);
		`,
		"continue runs the post": `
			for i := 0; i < 3; i++ {
				touch(i);
				continue;
				poke();
			}
		`,
		"break in a cond loop": `
			b := budget(5);
			for b.More() {
				poke();
				break;
			}
		`,
		"flat scope after the loop": `
			for i := 0; i < 3; i++ {
				poke();
			}
			touch(i);
		`,
		"body moves the variable": `
			for i := 0; i < 6; i++ {
				touch(i);
				i = bump(i);
			}
		`,
		"nested three-clause": `
			for i := 0; i < 2; i++ {
				for j := 0; j < 2; j++ {
					touch(j);
				}
				touch(i);
			}
		`,
		"for around range": `
			for i := 0; i < 2; i++ {
				for _, s := range fields("x y") {
					rec(s);
				}
			}
		`,
		"range around for": `
			for range 2 {
				for i := 0; i < 2; i++ {
					touch(i);
				}
			}
		`,
		"unsigned wrap": `
			u := u8v();
			for i := u; i != 3; i++ {
				poke();
			}
		`,
		"unsigned literal": `
			u := u8v();
			for i := u; i < 253; i++ {
				poke();
			}
		`,
		"signed wrap": `
			x := i8v();
			for i := x; i != -128; i++ {
				poke();
			}
		`,
		"error stops the body": `
			for i := 0; i < 3; i++ {
				touch(i);
				fail("x");
			}
		`,
		"error in the condition": `
			for boom() {
				poke();
			}
		`,
	} {
		assertLoopPair(t, rt, seen, name, src, false)
	}
}

// TestForContextBounds pins the termination guarantee: neither form
// has a data bound, so a run whose context dies stops at the next
// iteration boundary on both tiers.
func TestForContextBounds(t *testing.T) {
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
	if err := rt.Bind("yes", func() bool { return true }); err != nil {
		t.Fatal(err)
	}

	for name, src := range map[string]string{
		"condition never false": `
			for yes() {
				spin();
			}
		`,
		"comparison never false": `
			for i := 0; i != -1; i++ {
				spin();
			}
		`,
	} {
		jit, slow := compilePair(t, rt, src)
		for tier, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
			var ctx context.Context
			ctx, cancel = context.WithCancel(context.Background())
			calls = 0
			_, err := fn(ctx, nil, nil)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s/%s: err = %v, want context.Canceled", name, tier, err)
			}
			if calls != 3 {
				t.Errorf("%s/%s: the loop ran %d iterations past the cancel", name, tier, calls-3)
			}
			cancel()
		}
	}
}

// TestForSupports pins the tier: both header forms are direct when
// their calls are, and the inherited limits decline by name.
func TestForSupports(t *testing.T) {
	rt, _ := forRuntime(t)
	for name, src := range map[string]string{
		"three-clause": `for i := 0; i < 3; i++ { touch(i) }`,
		"cond method":  `b := mk(); for b.More() { poke() }`,
		"cond field":   `g := gated(); for g.Open { g.Tick() }`,
	} {
		if err := rt.Supports(src); err != nil {
			t.Errorf("%s should be direct: %v", name, err)
		}
	}
	if err := rt.Supports(`w := wrap(); for w.In.OK { poke() }`); err == nil {
		t.Error("a two-field condition should decline the direct tier")
	} else if !strings.Contains(err.Error(), "not in the table") {
		t.Errorf("the reason does not name the rule: %v", err)
	}
}

// BenchmarkForAliasing measures where the write-once aliasing rule
// turns off for the new loop forms: the same four calls passing a
// string to an any parameter, once from a write-once slot that
// aliases the frame and once from a slot a three-clause body rewrites
// per iteration, which boxes per call the way the Go compiler boxes
// at the same site.
func BenchmarkForAliasing(b *testing.B) {
	rt := NewRuntime()
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
			for i := 0; i < 4; i++ {
				s := one();
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
