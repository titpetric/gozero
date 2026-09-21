package gozero

import (
	"testing"
	"time"
)

// compileVM compiles a source to the reflect tier's program, which is
// what the frame post-pass runs over.
func compileVM(t *testing.T, rt *Runtime, src string) *vmProgram {
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

// frameWindows claims every call's window in the program tree and
// reports the words claimed, failing on the first overlap. Two calls
// sharing a word is the bug the post-pass exists to prevent: the
// nested call would overwrite the arguments its parent is filling.
func frameWindows(t *testing.T, p *vmProgram) map[int]bool {
	t.Helper()
	used := map[int]bool{}
	var walkCall func(c *vmCall)
	var walkArg func(a *vmArg)
	var walkStmts func(stmts []vmStmt)

	walkCall = func(c *vmCall) {
		for i := range c.args {
			at := c.off + i
			if used[at] {
				t.Errorf("%s: frame word %d is claimed twice", c.name, at)
			}
			used[at] = true
			walkArg(c.args[i])
		}
	}
	walkArg = func(a *vmArg) {
		for a != nil && a.kind == vaField {
			a = a.src
		}
		if a != nil && a.kind == vaCall {
			walkCall(a.sub)
		}
	}
	walkStmts = func(stmts []vmStmt) {
		for i := range stmts {
			s := &stmts[i]
			if s.call != nil {
				walkCall(s.call)
			}
			if s.assign != nil {
				walkArg(s.assign)
			}
			if s.retArg != nil {
				walkArg(s.retArg)
			}
			if s.ifs != nil {
				for _, a := range s.ifs.condArgs() {
					walkArg(a)
				}
				walkStmts(s.ifs.then)
				walkStmts(s.ifs.els)
			}
			if s.rng != nil {
				walkArg(s.rng.over)
				walkStmts(s.rng.body)
			}
		}
	}
	walkStmts(p.stmts)
	return used
}

// TestAssignFrameWindows pins the post-pass's contract on a nested
// call: every call holds a window of exactly its argument count and
// no two windows overlap, which is what lets one allocation carry
// the arguments of a whole program.
func TestAssignFrameWindows(t *testing.T) {
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"one": func(s string) string { return s },
		"two": func(a, b string) string { return a + b },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	p := compileVM(t, rt, `s := two(one("a"), one("b")); return s;`)
	if used := len(frameWindows(t, p)); used != p.frame {
		t.Errorf("%d words claimed, frame is %d", used, p.frame)
	}
	if p.frame != 4 {
		t.Errorf("frame = %d, want 4 for two(one, one)", p.frame)
	}
}

// TestAssignFrameHeaderOperands pins that the walk descends into an
// if header. A comparison evaluates two operands, so an operand call
// needs its own window exactly like a call in a statement does; a
// walk reaching only the single bool condition would leave the
// operand pointing at word zero, on top of another call's arguments.
func TestAssignFrameHeaderOperands(t *testing.T) {
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"time.Now":   time.Now,
		"time.Since": time.Since,
		"tag":        func(s string) string { return s },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.BindValue("time.Hour", time.Hour); err != nil {
		t.Fatal(err)
	}
	p := compileVM(t, rt, `u := tag("x"); t := time.Now(); s := "stale"; if time.Since(t) < time.Hour { s = tag(u); }; return s;`)

	var ifs *vmIf
	for i := range p.stmts {
		if p.stmts[i].ifs != nil {
			ifs = p.stmts[i].ifs
		}
	}
	if ifs == nil || ifs.cmp == nil {
		t.Fatal("the program has no header comparison")
	}
	if got := len(ifs.condArgs()); got != 2 {
		t.Fatalf("condArgs = %d, want the comparison's two operands", got)
	}
	if lhs := ifs.cmp.lhs; lhs.kind != vaCall {
		t.Fatalf("the left operand is %v, want a call", lhs.kind)
	}
	if used := len(frameWindows(t, p)); used != p.frame {
		t.Errorf("%d words claimed, frame is %d", used, p.frame)
	}
	if p.frame != 3 {
		t.Errorf("frame = %d, want one word each for tag, time.Since and tag", p.frame)
	}
}
