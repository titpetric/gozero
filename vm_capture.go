package gozero

import (
	"fmt"
	"reflect"
)

// Capture resolution for func literals. A name the body reads or
// assigns that belongs to the enclosing program is captured by cell:
// the walker below finds every such name before the body compiles,
// declareCaps seeds the body's slots with the cells, and
// finishFuncLits pins the enclosing side after the whole program
// compiled.

// capRec is one captured name: where it lives in the enclosing
// program and the static type its cell holds.
type capRec struct {
	name string
	slot int
	typ  reflect.Type
}

// declareCaps seeds a body's captured names as typed slots inside
// compileProgramWith, after the parameters. Each is address-taken on
// arrival: its storage is the enclosing run's cell, and every write
// must go through it.
func declareCaps(caps []capRec, p *vmProgram, newSlot func(string, reflect.Type) int) {
	for _, cr := range caps {
		slot := newSlot(cr.name, cr.typ)
		p.capSlots = append(p.capSlots, slot)
		p.addrTaken[slot] = true
	}
}

// funcLitCaptures resolves every name the body uses before it
// compiles: a parameter or a name the body defines is local, an
// enclosing program's name becomes a capture, and anything else is an
// error here, where the message can name the rule, rather than the
// silent zero an unknown stack name reads as at the top level. The
// returned records are in first-use order, so the capture list is
// deterministic.
func (c *Compiler) funcLitCaptures(outerSlots map[string]int, outerEnv map[string]reflect.Type, fl *funcLit) ([]capRec, error) {
	var caps []capRec
	seen := map[string]bool{}
	resolveOuter := func(name string) error {
		if seen[name] {
			return nil
		}
		slot, ok := outerSlots[name]
		if !ok {
			return fmt.Errorf("func literal: %s is neither a parameter, a name the body defines, nor a name of the enclosing program", name)
		}
		t := outerEnv[name]
		if t == nil {
			return fmt.Errorf("func literal: %s has no static type to capture", name)
		}
		seen[name] = true
		caps = append(caps, capRec{name: name, slot: slot, typ: t})
		return nil
	}
	if err := c.walkLitBody(fl, resolveOuter); err != nil {
		return nil, err
	}
	return caps, nil
}

// walkLitBody walks one literal's body in statement order, resolving
// reads and writes against the body's own names first and handing
// misses to outer, which captures in the enclosing scope or reports
// the name unknown. Statement order matters twice: a := or var of a
// name the body already captured is rejected, one name for two
// variables, and a := or var seen before any use shadows the
// enclosing name without capturing it, which is Go's rule for a
// declaration in an inner scope.
func (c *Compiler) walkLitBody(fl *funcLit, outer func(string) error) error {
	defined := map[string]bool{}
	for _, p := range fl.params {
		defined[p] = true
	}
	captured := map[string]bool{}
	resolve := func(n string) error {
		if defined[n] || captured[n] {
			return nil
		}
		if err := outer(n); err != nil {
			return err
		}
		captured[n] = true
		return nil
	}
	define := func(n string) error {
		if captured[n] {
			return fmt.Errorf("func literal: %s is declared after the body captured it; a name is either a capture or a local, not both", n)
		}
		defined[n] = true
		return nil
	}
	// A plain = to a name the body does not define assigns the
	// enclosing variable through its cell, so it captures; a name that
	// is not enclosing either falls through to the body compiler,
	// whose error names the := rule.
	assign := func(n string) {
		if defined[n] || captured[n] {
			return
		}
		if err := outer(n); err == nil {
			captured[n] = true
		}
	}
	var walkArg func(a *arg) error
	var walkCall func(e *callExpr) error
	walkArg = func(a *arg) error {
		switch a.kind {
		case argVar:
			return resolve(a.str)
		case argPath:
			return resolve(a.path[0])
		case argCall:
			return walkCall(a.sub)
		case argStruct:
			// The path names a type, not a value; only the elements
			// read names.
			for i := range a.elems {
				if err := walkArg(&a.elems[i].val); err != nil {
					return err
				}
			}
		case argRecv:
			return walkArg(a.recv)
		case argFuncLit:
			// A nested literal's enclosing scope is this body; a miss
			// here captures transitively, so the cell travels through
			// every literal between its owner and its reader.
			return c.walkLitBody(a.fn, resolve)
		}
		return nil
	}
	walkCall = func(e *callExpr) error {
		// The longest binding prefix wins, as in compileExpr; only when
		// no prefix names a binding is the head a name of the body.
		bound := false
		for i := len(e.path); i >= 1; i-- {
			if _, ok := c.bindings[joinPath(e.path[:i])]; ok {
				bound = true
				break
			}
		}
		if !bound {
			if err := resolve(e.path[0]); err != nil {
				return err
			}
		}
		for i := range e.args {
			if err := walkArg(&e.args[i]); err != nil {
				return err
			}
		}
		for _, l := range e.chain {
			for i := range l.args {
				if err := walkArg(&l.args[i]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for i := range fl.body.stmts {
		s := &fl.body.stmts[i]
		if s.varType != "" {
			if err := define(s.varName); err != nil {
				return err
			}
			continue
		}
		if s.fieldLhs != nil {
			if err := resolve(s.fieldLhs[0]); err != nil {
				return err
			}
		}
		if s.sendCh != nil {
			if err := resolve(s.sendCh[0]); err != nil {
				return err
			}
		}
		if s.call != nil {
			if err := walkCall(s.call); err != nil {
				return err
			}
		}
		if s.lit != nil {
			if err := walkArg(s.lit); err != nil {
				return err
			}
		}
		if s.retVal != nil {
			if err := walkArg(s.retVal); err != nil {
				return err
			}
		}
		if s.sendVal != nil {
			if err := walkArg(s.sendVal); err != nil {
				return err
			}
		}
		for _, n := range s.lhs {
			if s.define {
				if err := define(n); err != nil {
					return err
				}
			} else {
				assign(n)
			}
		}
	}
	return nil
}

// finishFuncLits is the capture post-pass: every slot a literal
// captured is marked address-taken, so run stores it through a cell
// the closure shares, the step JIT refuses to splice or alias it, and
// a slot the program reassigns at a different type after handing its
// cell out is rejected, because no one cell could hold both.
func (p *vmProgram) finishFuncLits() error {
	var walkArg func(a *vmArg) error
	walkCall := func(c *vmCall) error {
		for _, a := range c.args {
			if err := walkArg(a); err != nil {
				return err
			}
		}
		return nil
	}
	walkArg = func(a *vmArg) error {
		if a == nil {
			return nil
		}
		if fl := a.funclit; fl != nil {
			for i, slot := range fl.capOuter {
				p.addrTaken[slot] = true
				if p.slotTypes[slot] != fl.capTypes[i] {
					return fmt.Errorf("compile: %s: a captured name is reassigned from %s to %s, and the captured cell holds one type", fl.name, fl.capTypes[i], p.slotTypes[slot])
				}
			}
		}
		for a.kind == vaField {
			a = a.src
		}
		switch a.kind {
		case vaCall:
			return walkCall(a.sub)
		case vaStruct:
			for i := range a.elems {
				if err := walkArg(a.elems[i].val); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for i := range p.stmts {
		s := &p.stmts[i]
		if s.call != nil {
			if err := walkCall(s.call); err != nil {
				return err
			}
		}
		for _, a := range []*vmArg{s.assign, s.retArg} {
			if err := walkArg(a); err != nil {
				return err
			}
		}
		if s.fieldSet != nil {
			if err := walkArg(s.fieldSet.val); err != nil {
				return err
			}
		}
		if s.recv != nil {
			if err := walkArg(s.recv.ch); err != nil {
				return err
			}
		}
		if s.send != nil {
			if err := walkArg(s.send.ch); err != nil {
				return err
			}
			if err := walkArg(s.send.val); err != nil {
				return err
			}
		}
	}
	return nil
}
