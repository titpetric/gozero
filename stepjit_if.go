package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// Conditions on the direct tier, and the structural plan every
// construct with a braced body shares. An if compiles to a structured
// node holding one []nodeE per arm: no program counter, no jump, the
// arm runs front to back exactly like the program's own list, and the
// only exit is an error. A return inside an arm raises
// errProgramReturn on that error return, leaving its value in a
// hidden frame field that run reads back. A range loop (stepjit_range.go)
// is the same shape with the body as its list.
//
// A program with either takes the structural plan below instead of
// planInline: statements keep their nesting and nothing splices,
// because findSplice cannot prove a producer's single reader runs
// when a branch or a loop boundary sits between them. Slots written
// inside a nested list are counted as written twice, so the
// write-once interface aliasing in argNode never fires for a
// maybe-written or loop-carried slot; that is the allocation cost
// docs/design/conditions.md and docs/design/loops.md predict, and
// BenchmarkIfWriteCount and BenchmarkRangeAliasing measure it.

// hasBlocks reports a construct with a nested statement list in the
// program's top-level list. A nested one sits inside another, so the
// top level is enough.
func hasBlocks(stmts []vmStmt) bool {
	for i := range stmts {
		if stmts[i].ifs != nil || stmts[i].rng != nil || stmts[i].fors != nil {
			return true
		}
	}
	return false
}

// planBlocks is the structural plan for a program with a nested
// statement list.
func planBlocks(p *vmProgram) (*jitPlan, error) {
	plan := &jitPlan{
		live:    map[int]bool{},
		writes:  map[int]int{},
		splices: map[*vmArg]*vmCall{},
		retSlot: -1,
	}
	last := len(p.stmts) - 1
	for i := range p.stmts {
		s := &p.stmts[i]
		if s.retArg != nil {
			if i != last {
				return nil, fmt.Errorf("a return before the last statement is not a straight line")
			}
			// Only a name that already has a slot returns from the top
			// level, the same rule planInline applies.
			if s.retArg.kind != vaSlot {
				return nil, fmt.Errorf("a returned expression is not in the table")
			}
			plan.retSlot = s.retArg.slot
			plan.live[plan.retSlot] = true
			continue
		}
		if s.ret && s.call == nil {
			// A bare "return;" leaves the program without a value; in
			// any position but the last it is an early exit the flat
			// statement list cannot express.
			if i != last {
				return nil, fmt.Errorf("a return before the last statement is not a straight line")
			}
			continue
		}
		if s.ret && i != last {
			return nil, fmt.Errorf("a return before the last statement is not a straight line")
		}
		ps, err := planBlockStmt(s)
		if err != nil {
			return nil, err
		}
		plan.stmts = append(plan.stmts, ps)
	}
	// A var declaration puts a name in scope whether or not anything
	// assigns it; the zeroed frame is its value.
	for _, in := range p.inits {
		plan.live[in.slot] = true
		plan.writes[in.slot]++
	}
	// A func literal body's parameters are written at entry, the
	// same one write planInline counts for them.
	for _, slot := range p.params {
		plan.live[slot] = true
		plan.writes[slot]++
	}
	blockCounters(p, p.stmts, false, plan)
	return plan, nil
}

// planBlockStmt converts one statement for the structural plan. It is
// planInline's per-statement mapping without the splice pass; an if
// and a loop travel whole, and their nested lists convert again when
// the node builder reaches them.
func planBlockStmt(s *vmStmt) (plannedStmt, error) {
	out := -1
	if len(s.out) > 0 {
		out = s.out[0]
	}
	switch {
	case s.ifs != nil:
		return plannedStmt{ifs: s.ifs, out: -1}, nil
	case s.rng != nil:
		return plannedStmt{rng: s.rng, out: -1}, nil
	case s.fors != nil:
		return plannedStmt{fors: s.fors, out: -1}, nil
	case s.brk || s.cont:
		return plannedStmt{brk: s.brk, cont: s.cont, out: -1}, nil
	case s.assign != nil:
		return plannedStmt{assign: s.assign, out: out}, nil
	case s.fieldSet != nil:
		return plannedStmt{fieldSet: s.fieldSet, out: -1}, nil
	case s.recv != nil:
		return plannedStmt{recv: s.recv, out: out}, nil
	case s.send != nil:
		return plannedStmt{send: s.send, out: -1}, nil
	case s.inc != nil:
		return plannedStmt{inc: s.inc, out: -1}, nil
	case s.lit.IsValid():
		return plannedStmt{lit: s.lit, out: out}, nil
	case s.call != nil:
		if s.ret && s.call.nres > 0 && out < 0 {
			return plannedStmt{}, fmt.Errorf("a returned value needs a slot")
		}
		return plannedStmt{call: s.call, out: out, ret: s.ret}, nil
	}
	return plannedStmt{}, fmt.Errorf("this statement is not in the table")
}

// blockCounters fills writes and live over the statement tree.
// nested bumps every write by two: a slot assigned inside an arm is
// maybe-written and one assigned inside a loop body is written once
// per iteration, so neither may count as written exactly once, which
// is the condition the interface aliasing in argNode needs. The count
// is conservative on purpose; the price is a copy where a
// straight-line program would alias, never a wrong value. The walk
// also finds the returns inside arms, which is what asks for the
// signal field.
func blockCounters(p *vmProgram, stmts []vmStmt, nested bool, plan *jitPlan) {
	bump := 1
	if nested {
		bump = 2
	}
	for i := range stmts {
		s := &stmts[i]
		// Only the first bound result travels on this tier, the same
		// single out plannedStmt carries: a second name stays without
		// a slot and a read of it declines the program.
		if len(s.out) > 0 && s.out[0] >= 0 {
			plan.writes[s.out[0]] += bump
			plan.live[s.out[0]] = true
		}
		if s.inc != nil {
			plan.writes[s.inc.slot] += bump
			plan.live[s.inc.slot] = true
		}
		if s.fieldSet != nil {
			plan.live[s.fieldSet.base] = true
			// Writing a field of a struct held by value mutates the
			// slot an aliased interface may point at, so it counts as
			// a write like it does in planInline.
			if t := p.slotTypes[s.fieldSet.base]; t != nil && t.Kind() != reflect.Pointer {
				plan.writes[s.fieldSet.base] += bump
			}
		}
		if s.retArg != nil && s.retArg.kind == vaSlot {
			plan.live[s.retArg.slot] = true
		}
		if nested && s.ret {
			plan.retSignal = true
		}
		if s.ifs != nil {
			blockCounters(p, s.ifs.then, true, plan)
			blockCounters(p, s.ifs.els, true, plan)
		}
		if s.rng != nil {
			// The loop variable is written once per iteration by
			// definition, so it takes the same two as a body slot.
			for _, slot := range [2]int{s.rng.keySlot, s.rng.valSlot} {
				if slot >= 0 {
					plan.live[slot] = true
					plan.writes[slot] += 2
				}
			}
			blockCounters(p, s.rng.body, true, plan)
		}
		if s.fors != nil {
			// The loop variable is written by the init and again by
			// the post clause of every iteration, so it takes the
			// same two a range key does.
			if slot := s.fors.initSlot; slot >= 0 {
				plan.live[slot] = true
				plan.writes[slot] += 2
			}
			blockCounters(p, s.fors.body, true, plan)
		}
	}
}

// ifNode compiles an if chain: the condition as bool bits, each arm
// its own statement list.
func (c *jitCompiler) ifNode(n *vmIf, jp *jitProgram) (nodeE, error) {
	var cond nodeN
	var err error
	if n.cmp != nil {
		cond, err = c.cmpNode(n.cmp)
	} else {
		cond, err = c.condNode(n.cond)
	}
	if err != nil {
		return nil, err
	}
	then, err := c.blockNodes(n.then, jp)
	if err != nil {
		return nil, err
	}
	els, err := c.blockNodes(n.els, jp)
	if err != nil {
		return nil, err
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		v, err := cond(fr, ctx, st, d)
		if err != nil {
			return err
		}
		arm := els
		if v != 0 {
			arm = then
		}
		for _, stmt := range arm {
			if err := stmt(fr, ctx, st, d); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

// blockNodes compiles one nested statement list, an if arm or a loop
// body, through the same stmtNode path the top level takes. A return
// compiles to the signal raise instead; the statements written after
// it in the same list still compile and never run, exactly as the
// reflect evaluator never reaches them.
func (c *jitCompiler) blockNodes(stmts []vmStmt, jp *jitProgram) ([]nodeE, error) {
	if len(stmts) == 0 {
		return nil, nil
	}
	out := make([]nodeE, 0, len(stmts))
	for i := range stmts {
		s := &stmts[i]
		if s.ret {
			n, err := c.returnNode(s, jp)
			if err != nil {
				return nil, err
			}
			out = append(out, n)
			continue
		}
		ps, err := planBlockStmt(s)
		if err != nil {
			return nil, err
		}
		n, err := c.stmtNode(ps, jp)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// returnNode compiles a return inside an arm. The value is boxed
// into the program's signal field and errProgramReturn travels the
// error return up to jitProgram.run, which is the only consumer.
func (c *jitCompiler) returnNode(s *vmStmt, jp *jitProgram) (nodeE, error) {
	sigOff := jp.retSigOff
	if s.retArg != nil {
		if s.retArg.kind != vaSlot {
			return nil, fmt.Errorf("a returned expression is not in the table")
		}
		field, ok := c.slotOf[s.retArg.slot]
		if !ok {
			return nil, fmt.Errorf("a returned name has no slot")
		}
		t, off := c.types[field], c.offs[field]
		return func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) error {
			*(*any)(unsafe.Add(fr, sigOff)) = reflect.NewAt(t, unsafe.Add(fr, off)).Elem().Interface()
			return errProgramReturn
		}, nil
	}
	if s.call == nil {
		// A bare return: the signal field stays zero, so the program
		// hands back a nil value.
		return func(unsafe.Pointer, context.Context, map[string]any, any) error {
			return errProgramReturn
		}, nil
	}
	if s.call.nres > 0 {
		return nil, fmt.Errorf("a returned call value inside an if arm is not in the table")
	}
	n, err := c.exprNode(s.call)
	if err != nil {
		return nil, err
	}
	run, err := c.dropped(n)
	if err != nil {
		return nil, err
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		if err := run(fr, ctx, st, d); err != nil {
			return err
		}
		return errProgramReturn
	}, nil
}

// condShapes extends the shape table with the predicate shapes
// conditions call, consulted by callNode after its own cases.
func condShapes(key string, fptr unsafe.Pointer, a []node) (node, bool) {
	switch key {
	case "i64_b":
		f, a0 := castFn[func(int64) bool](fptr), a[0].N
		return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			n0, err := a0(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			if f(int64(n0)) {
				return 1, nil
			}
			return 0, nil
		}}, true
	case "SS_b":
		f, a0, a1 := castFn[func(string, string) bool](fptr), a[0].S, a[1].S
		return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			s0, err := a0(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			s1, err := a1(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			if f(s0, s1) {
				return 1, nil
			}
			return 0, nil
		}}, true
	}
	return node{}, false
}

// condNode compiles the condition to the bool bits the arms test.
func (c *jitCompiler) condNode(a *vmArg) (nodeN, error) {
	var n node
	var err error
	if a.kind == vaCall {
		n, err = c.exprNode(a.sub)
	} else {
		n, err = c.argNode(a, a.typ, lBool)
	}
	if err != nil {
		return nil, err
	}
	if n.class != lBool {
		return nil, fmt.Errorf("an if condition of class %s is not in the table", n.class)
	}
	return n.N, nil
}
