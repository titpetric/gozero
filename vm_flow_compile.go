package gozero

import (
	"fmt"
	"reflect"
)

// progCompiler carries the statement-compiling machinery compileOne
// builds up in compileProgram, so the control-flow cases can live
// beside the flow IR instead of inside one long function.
type progCompiler struct {
	c        *Compiler
	p        *vmProgram
	prog     *program
	reserved map[string]bool
	// loopDepth tracks how many loop bodies enclose the statement
	// being compiled, for the break and continue placement checks.
	loopDepth int
	// fn is the unit being compiled, nil for the top-level program;
	// return statements compile against its declared results.
	fn *fnCompile
}

// checkName rejects a name that would shadow a binding or keyword,
// because path resolution prefers the binding and the name could
// never be read back.
func (pc *progCompiler) checkName(name string) error {
	if pc.reserved[name] {
		return fmt.Errorf("compile: %s shadows a binding or keyword and cannot be assigned", name)
	}
	return nil
}

// checkDecl enforces Go's declaration rule: = assigns to a name that
// already exists somewhere in the scope chain.
func (pc *progCompiler) checkDecl(sc *cscope, name string, define bool) error {
	if _, ok := sc.slot(name); !ok && !define {
		return fmt.Errorf("compile: %s is not defined, use := or var", name)
	}
	return nil
}

// checkNew is the other half: := must declare something new in the
// innermost scope; shadowing an outer name counts, as in Go.
func (pc *progCompiler) checkNew(lhs []string, sc *cscope) error {
	for _, name := range lhs {
		if name == "_" {
			continue // the blank identifier declares nothing
		}
		if _, ok := sc.slots[name]; !ok {
			return nil
		}
	}
	return fmt.Errorf("compile: no new variables on left side of :=")
}

// newSlot resolves or allocates the slot behind a name. := looks
// only at the innermost scope, so it shadows an outer name with a
// fresh slot; = walks the chain to the owner.
func (pc *progCompiler) newSlot(sc *cscope, name string, t reflect.Type, define bool) int {
	p := pc.p
	owner := sc
	var slot int
	var ok bool
	if define {
		slot, ok = sc.slots[name]
	} else {
		for o := sc; o != nil; o = o.parent {
			if slot, ok = o.slots[name]; ok {
				if o.fn != sc.fn && sc.fn != nil {
					// Assigning across a unit boundary writes through
					// the captured cell; the type is the owner's and
					// stays fixed.
					return sc.fn.capture(o.fn, slot, o.env[name])
				}
				owner = o
				break
			}
		}
	}
	if !ok {
		slot = p.nslots
		p.nslots++
		p.slotTypes = append(p.slotTypes, nil)
		owner.slots[name] = slot
	}
	if prev := p.slotTypes[slot]; prev != nil && prev != t {
		p.polymorphic = true
	}
	p.slotTypes[slot] = t
	owner.env[name] = t
	return slot
}

// compileFlow compiles an if chain or any of the loop forms.
func (pc *progCompiler) compileFlow(sc *cscope, s stmt, dst *[]vmStmt) error {
	if s.ifs != nil {
		cond, ct, err := pc.c.compileValueExpr(sc, s.ifs.cond, boolType)
		if err != nil {
			return err
		}
		if ct.Kind() != reflect.Bool {
			return fmt.Errorf("compile: the if condition must be bool, got %s", sc.typeName(ct))
		}
		node := &vmIf{cond: cond}
		if err := pc.compileBlock(sc.child(), s.ifs.then.stmts, &node.then.stmts); err != nil {
			return err
		}
		if s.ifs.els != nil {
			node.els = &vmBlock{}
			if err := pc.compileBlock(sc.child(), s.ifs.els.stmts, &node.els.stmts); err != nil {
				return err
			}
		}
		*dst = append(*dst, vmStmt{ifs: node})
		return nil
	}
	if s.fors != nil {
		f := s.fors
		fsc := sc.child()
		if f.rangeX != nil {
			over, ot, err := pc.c.compileValueExpr(fsc, *f.rangeX, nil)
			if err != nil {
				return err
			}
			rng := &vmRange{over: over, keySlot: -1, valSlot: -1}
			var kt, vt reflect.Type
			switch ot.Kind() {
			case reflect.Slice, reflect.Array:
				kt, vt = intType, ot.Elem()
			case reflect.String:
				// Ranging a string yields byte index and rune, as
				// in Go.
				kt, vt = intType, reflect.TypeFor[rune]()
			case reflect.Map:
				kt, vt = ot.Key(), ot.Elem()
			case reflect.Chan:
				// A channel range has one variable, the element, and
				// runs until the channel closes, as in Go; a blocked
				// receive ends with the execution context, like every
				// channel operation here.
				if f.val != "" && f.val != "_" {
					return fmt.Errorf("compile: a channel range has one variable")
				}
				if ot.ChanDir() == reflect.SendDir {
					return fmt.Errorf("compile: cannot range over a send-only channel")
				}
				kt, vt = ot.Elem(), nil
			default:
				return fmt.Errorf("compile: cannot range over %s", sc.typeName(ot))
			}
			if f.key != "" && f.key != "_" {
				if err := pc.checkName(f.key); err != nil {
					return err
				}
				rng.keySlot = pc.newSlot(fsc, f.key, kt, true)
			}
			if f.val != "" && f.val != "_" {
				if err := pc.checkName(f.val); err != nil {
					return err
				}
				rng.valSlot = pc.newSlot(fsc, f.val, vt, true)
			}
			rng.overChan = ot.Kind() == reflect.Chan
			pc.loopDepth++
			err = pc.compileBlock(fsc.child(), f.body.stmts, &rng.body.stmts)
			pc.loopDepth--
			if err != nil {
				return err
			}
			*dst = append(*dst, vmStmt{rng: rng})
			return nil
		}
		loop := &vmFor{}
		if f.init != nil {
			if err := pc.compileOne(fsc, *f.init, &loop.init, false); err != nil {
				return err
			}
		}
		if f.cond != nil {
			cond, ct, err := pc.c.compileValueExpr(fsc, *f.cond, boolType)
			if err != nil {
				return err
			}
			if ct.Kind() != reflect.Bool {
				return fmt.Errorf("compile: the for condition must be bool, got %s", sc.typeName(ct))
			}
			loop.cond = cond
		}
		if f.post != nil {
			if err := pc.compileOne(fsc, *f.post, &loop.post, false); err != nil {
				return err
			}
		}
		pc.loopDepth++
		err := pc.compileBlock(fsc.child(), f.body.stmts, &loop.body.stmts)
		pc.loopDepth--
		if err != nil {
			return err
		}
		*dst = append(*dst, vmStmt{loop: loop})
		return nil
	}
	return nil
}
