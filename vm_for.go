package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// The condition and three-clause loops on the reflect tier. Both are
// header-scoped: the condition is a bool name, field or call read per
// iteration, and the three-clause header is exactly an init
// assignment, one comparison, and the loop variable stepped by one.
// Neither form carries a data bound the way range does, so the
// per-iteration ctx.Err() check is the only bound either keeps: a
// cancelled or expired execution context ends a spinning loop with
// its error.
//
// Nothing here is a new machine. The condition compiles through the
// if header's compileCond, the comparison through the comparison kit
// of vm_cmp.go, and the post clause is the vmInc a step statement
// compiles to, stepping the loop variable at its own width with the
// wrap compiled Go has. The body is a nested statement list run by
// runStmts, the same function the program's own list, an if arm and
// a range body go through.
//
// Scope stays flat. The loop variable is a program-level slot the way
// a range key is: one cell reused per iteration, defined after the
// loop with its last value, on both tiers.

// vmFor is a compiled condition or three-clause loop. cond is set on
// the condition form; initSlot, initVal, varType, cmp and post on the
// other.
type vmFor struct {
	cond *vmArg

	initSlot int // -1 on the condition form
	initVal  *vmArg
	varType  reflect.Type
	cmp      *vmCmp
	post     *vmInc

	body []vmStmt
}

// headerArgs is every argument the header evaluates, the way a
// condition header's condArgs is: the frame post-pass, the stack-read
// counter and the pool planner walk a loop header through it, so an
// operand call gets its frame window and its pools like any other.
func (f *vmFor) headerArgs() []*vmArg {
	if f.cond != nil {
		return []*vmArg{f.cond}
	}
	return []*vmArg{f.initVal, f.cmp.lhs, f.cmp.rhs}
}

// countsAt reports whether a three-clause header can count with a
// kind. Go steps floats too, and a step statement does, but a loop
// counter is an integer: a float bound compares by a rule the header
// has no way to state.
func countsAt(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	}
	return false
}

// compileFor compiles one condition or three-clause loop. The body
// compiles through compileStmts, the same per-list compiler the
// program and the if arms use, and the branch counter is deliberately
// not raised: a := inside a loop body is allowed and the name it
// declares outlives the loop, which is what flat scope means here.
func (pc *progCompiler) compileFor(fs *forStmt, dst *[]vmStmt) error {
	f := &vmFor{initSlot: -1}
	if fs.cond != nil {
		cond, err := pc.compileCond(*fs.cond, "loop condition")
		if err != nil {
			return err
		}
		f.cond = cond
	} else if err := pc.compileForHeader(fs, f); err != nil {
		return err
	}
	if err := pc.compileStmts(fs.body, &f.body); err != nil {
		return err
	}
	if err := pc.checkHeaderTypes(fs, f); err != nil {
		return err
	}
	*dst = append(*dst, vmStmt{fors: f})
	return nil
}

// compileForHeader compiles the three clauses. The slot exists before
// the comparison compiles, so the header can read the name it
// declares, and the post clause is the compiled form of the step
// statement the source wrote.
func (pc *progCompiler) compileForHeader(fs *forStmt, f *vmFor) error {
	if err := pc.checkName(fs.initName); err != nil {
		return err
	}
	iv, t, err := pc.forInit(fs)
	if err != nil {
		return err
	}
	if !countsAt(t.Kind()) {
		return fmt.Errorf("compile: the loop variable %s is %s, a three-clause for counts with an integer", fs.initName, t)
	}
	f.initVal, f.varType = iv, t
	f.initSlot = pc.newSlot(fs.initName, t)
	if f.cmp, err = pc.compileCmp(fs.cmp); err != nil {
		return err
	}
	delta := int64(1)
	if fs.postDec {
		delta = -1
	}
	f.post = &vmInc{slot: f.initSlot, delta: delta, name: fs.initName + incDecOp(delta)}
	return nil
}

// forInit resolves the init clause to an argument and the loop
// variable's static type. A name, a field or a call carries its own
// type; an integer literal adopts the type of the operand it is
// compared with, the rule an untyped constant follows on either side
// of a comparison, and keeps its own width when that side is a
// literal too.
func (pc *progCompiler) forInit(fs *forStmt) (*vmArg, reflect.Type, error) {
	if fs.initVal.kind != argInt {
		va, t, err := pc.cmpOperand(fs.initVal)
		if err != nil {
			return nil, nil, err
		}
		if t == nil {
			return nil, nil, fmt.Errorf("compile: a loop variable's init must be an integer")
		}
		return va, t, nil
	}
	t := pc.forLiteralType(fs)
	if !countsAt(t.Kind()) {
		// The caller names the rule: converting the literal into a
		// type nothing can count with would report the conversion.
		return nil, t, nil
	}
	v, err := literalAs(t, fs.initVal)
	if err != nil {
		return nil, nil, fmt.Errorf("compile: the loop variable %s: %w", fs.initName, err)
	}
	return &vmArg{kind: vaConst, val: v, typ: t, iface: -1}, t, nil
}

// forLiteralType is the type a literal init adopts: that of the
// comparison operand which is not the loop variable. The operand is
// compiled only to read its type off, and compileCmp compiles it
// again for the node it runs; an operand the comparison will reject
// falls back to the literal's own width, so the rule is named once,
// by the comparison.
func (pc *progCompiler) forLiteralType(fs *forStmt) reflect.Type {
	other := fs.cmp.rhs
	if !namesVar(fs.cmp.lhs, fs.initName) {
		other = fs.cmp.lhs
	}
	if _, t, err := pc.cmpOperand(other); err == nil && t != nil {
		return t
	}
	return reflect.TypeFor[int64]()
}

// namesVar reports an operand that is exactly the given name.
func namesVar(a arg, name string) bool {
	return a.kind == argVar && a.str == name
}

// checkHeaderTypes re-reads the header's names after the body
// compiled. A body may reassign one, but not away from the type the
// header runs on: the post clause counts an integer and the
// comparison reads its operands at one static type, so a reassigned
// name would be read through the wrong type and panic the reflect
// evaluator on the next iteration.
func (pc *progCompiler) checkHeaderTypes(fs *forStmt, f *vmFor) error {
	check := func(a arg, want reflect.Type, what string) error {
		if a.kind != argVar {
			// Only a name can be reassigned: a field path is read
			// through its own type and a call returns one.
			return nil
		}
		if t := pc.env[a.str]; t != want {
			return fmt.Errorf("compile: the %s %s is reassigned as %s inside the loop body", what, a.str, t)
		}
		return nil
	}
	if f.cond != nil {
		return check(*fs.cond, f.cond.typ, "loop condition")
	}
	if t := pc.env[fs.initName]; t != f.varType {
		return fmt.Errorf("compile: the loop variable %s is reassigned as %s inside the loop body", fs.initName, t)
	}
	if err := check(fs.cmp.lhs, f.cmp.typ, "comparison operand"); err != nil {
		return err
	}
	return check(fs.cmp.rhs, f.cmp.typ, "comparison operand")
}

// test evaluates the loop's condition or comparison for one
// iteration.
func (f *vmFor) test(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) (bool, error) {
	if f.cmp != nil {
		return f.cmp.test(ctx, slots, frame, ifaces, stack, dest)
	}
	v, err := f.cond.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return false, err
	}
	return v.IsValid() && v.Bool(), nil
}

// runFor executes one condition or three-clause loop. The loop
// variable's cell is written by the init and stepped by the post
// clause; a body that reassigns the name writes the same slot, and
// the post clause reads it back before stepping, so the reassignment
// counts.
func (p *vmProgram) runFor(ctx context.Context, f *vmFor, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) error {
	if f.initSlot >= 0 {
		v, err := f.initVal.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return err
		}
		p.setSlot(slots, v, f.initSlot)
	}
	for {
		// The context bounds every iteration: neither header has a
		// data bound, so this check is the only one either keeps.
		if err := ctx.Err(); err != nil {
			return err
		}
		more, err := f.test(ctx, slots, frame, ifaces, stack, dest)
		if err != nil || !more {
			return err
		}
		switch _, err := p.runStmts(ctx, slots, frame, ifaces, stack, dest, f.body); err {
		case nil, errLoopContinue:
		case errLoopBreak:
			return nil
		default:
			return err
		}
		if f.post != nil {
			// break returns above, so the post clause runs after a
			// completed body and after a continue, as in Go.
			if err := f.post.exec(slots, p.addrTaken[f.post.slot]); err != nil {
				return err
			}
		}
	}
}
