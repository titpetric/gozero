package gozero

import (
	"strings"
	"testing"
)

// planFor drives these through the public compile path: the plan is
// an internal artifact, so the tests compile a program and inspect
// the counts the planner produced.

// TestAccountForWrites pins the conservative write count: the loop
// variable and every slot a for body writes count at two, which is
// what turns the write-once aliasing rule off for them, while a slot
// written once before the loop keeps its single write.
func TestAccountForWrites(t *testing.T) {
	rt, _ := forRuntime(t)
	prog, err := (&Parser{}).Parse(`
		n := count();
		for i := 0; i < n; i++ {
			m := bump(i);
			touch(m);
		}
	`)
	if err != nil {
		t.Fatal(err)
	}
	p, err := rt.compiler.compileProgram(prog)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planInline(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"n": 1, "i": 2, "m": 2}
	slots := map[string]int{"n": 0, "i": 1, "m": 2}
	for name, w := range want {
		if got := plan.writes[slots[name]]; got != w {
			t.Errorf("writes[%s] = %d, want %d", name, got, w)
		}
		if !plan.live[slots[name]] {
			t.Errorf("%s is not live", name)
		}
	}
}

// TestCountForReads pins that the header's reads count: a producer
// whose slot only the comparison reads must keep its statement and
// slot rather than being spliced into a loop it cannot enter.
func TestCountForReads(t *testing.T) {
	rt, seen := forRuntime(t)
	src := `
		n := count();
		for i := 0; i < n; i++ {
			poke();
		}
	`
	jit, slow := compilePair(t, rt, src)
	for tier, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		*seen = nil
		if _, err := fn(t.Context(), nil, nil); err != nil {
			t.Fatalf("%s: %v", tier, err)
		}
		if len(*seen) != 3 {
			t.Errorf("%s: %d iterations, want 3", tier, len(*seen))
		}
	}
}

// TestPlanBodyRejects pins the planner mirror of the parser's body
// rules: a statement kind a body cannot hold stays a named error
// rather than compiling into a loop.
func TestPlanBodyRejects(t *testing.T) {
	body := []vmStmt{{ret: true}}
	if _, err := planBody(body); err == nil || !strings.Contains(err.Error(), "inside a loop body") {
		t.Errorf("err = %v, want the loop-body rule", err)
	}
}
