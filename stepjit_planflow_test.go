package gozero

import (
	"testing"
)

// TestPlanFlowRouting pins which plan a program takes and what the
// structural plan reports: flow, defers and error-binding calls all
// route structural, and the loop write counting turns off the
// write-once aliasing for loop-carried names.
func TestPlanFlowRouting(t *testing.T) {
	rt := exprRuntime(t)
	compile := func(src string) *vmProgram {
		t.Helper()
		prog, err := (&Parser{}).Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		p, err := rt.compiler.compileProgram(prog)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	p := compile(`n := 0; for n < 3 { n++ }; return n;`)
	plan, err := planInline(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.splices) != 0 {
		t.Errorf("a flow plan must not splice, got %d", len(plan.splices))
	}
	// The loop-carried slot counts as rewritten.
	rewritten := false
	for _, n := range plan.writes {
		if n > 1 {
			rewritten = true
		}
	}
	if !rewritten {
		t.Error("a loop-carried write must count at least twice")
	}

	p = compile(`defer pnil(); n := 1; return n;`)
	if plan, err = planInline(p); err != nil {
		t.Fatal(err)
	}
	if !plan.needsDefers {
		t.Error("a deferred call must mark needsDefers")
	}

	p = compile(`x := 1; if x > 0 { return "in" }; return "out";`)
	if plan, err = planInline(p); err != nil {
		t.Fatal(err)
	}
	if !plan.needsRetAny {
		t.Error("a return inside flow must mark needsRetAny")
	}
	if plan.retSlot >= 0 {
		t.Error("an expression trailing return must not claim the typed slot")
	}
}
