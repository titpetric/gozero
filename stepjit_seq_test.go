package gozero

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// TestSeqMatchesReflect runs range-over-func in both shapes through
// both tiers: the yield closure on the direct tier against
// reflect.Value.Seq and Seq2 on the reflect one.
func TestSeqMatchesReflect(t *testing.T) {
	rt, seen := loopRuntime(t)

	for name, src := range map[string]string{
		"seq values": `
			for s := range lines("a\nb\n") {
				rec(s);
			}
		`,
		"seq break": `
			for s := range lines("a\nb\n") {
				rec(s);
				break;
			}
			poke();
		`,
		"seq continue skips": `
			for s := range lines("a\nb\n") {
				rec(s);
				continue;
				poke();
			}
		`,
		"seq bare": `
			for range lines("a\nb\n") {
				poke();
			}
		`,
		"seq name outlives the loop": `
			for s := range lines("one\n") {
			}
			rec(s);
		`,
		"seq error mid-loop": `
			for s := range lines("a\nb\n") {
				rec(s);
				fail(s);
			}
		`,
		"seq2 pairs": `
			xs := fields("p q r");
			for i, s := range pairs(xs) {
				idx(i);
				rec(s);
			}
		`,
		"seq2 key only": `
			xs := fields("p q");
			for i := range pairs(xs) {
				idx(i);
			}
		`,
		"seq2 value only": `
			xs := fields("p q");
			for _, s := range pairs(xs) {
				rec(s);
			}
		`,
		"seq2 break": `
			xs := fields("p q r");
			for i, s := range pairs(xs) {
				rec(s);
				break;
			}
			idx(i);
		`,
		"seq from a slot": `
			f := lines("x\ny\n");
			for s := range f {
				rec(s);
			}
		`,
	} {
		assertLoopPair(t, rt, seen, name, src, false)
	}
}

// TestRogueIteratorPanics pins the misbehaving-iterator contract on
// both tiers: an iterator that keeps yielding after its consumer said
// stop panics, as Go's own range-over-func does, instead of writing
// into a frame the loop has left. The panic arrives as *PanicError
// through the guard.
func TestRogueIteratorPanics(t *testing.T) {
	rt, seen := loopRuntime(t)
	src := `
		for s := range rogue() {
			rec(s);
			break;
		}
	`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("the rogue program should JIT: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	*seen = nil
	_, err = fn.ExecContext[any](context.Background(), nil)
	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a *PanicError", err)
	}
	if got := fmt.Sprintf("%v", *seen); got != "[a]" {
		t.Errorf("effects = %s, want [a]", got)
	}
}

// TestSeqSupports pins the tier of both iterator shapes, and that a
// yield type with no layout class names its reason while the reflect
// evaluator still runs the loop.
func TestSeqSupports(t *testing.T) {
	rt, seen := loopRuntime(t)
	if err := rt.Bind("urls", func() func(func(url.URL) bool) {
		return func(yield func(url.URL) bool) {
			yield(url.URL{Path: "/a"})
		}
	}); err != nil {
		t.Fatal(err)
	}

	for name, src := range map[string]string{
		"seq":  `for s := range lines("a\n") { rec(s); continue; poke() }`,
		"seq2": `xs := fields("a b"); for i, s := range pairs(xs) { idx(i); rec(s); break }`,
	} {
		if err := rt.Supports(src); err != nil {
			t.Errorf("%s should be a direct program: %v", name, err)
		}
	}

	src := `
		for u := range urls() {
			poke();
		}
	`
	err := rt.Supports(src)
	if err == nil {
		t.Fatal("a struct yield should decline the direct tier")
	}
	if !strings.Contains(err.Error(), "not in the table") {
		t.Errorf("the reason does not name the gap: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	*seen = nil
	if _, err := fn.ExecContext[any](context.Background(), nil); err != nil {
		t.Errorf("the reflect evaluator should run it: %v", err)
	}
	if got := fmt.Sprintf("%v", *seen); got != "[poke]" {
		t.Errorf("effects = %s, want [poke]", got)
	}
}
