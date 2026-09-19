package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// Conditions on the direct tier. An if compiles to a structured node
// holding one []nodeE per arm: no program counter, no jump, the arm
// runs front to back exactly like the program's own list, and the
// only exit is an error, which the compiler guarantees by rejecting
// a return inside an arm.
//
// A program with an if takes the structural plan below instead of
// planInline: statements keep their nesting and nothing splices,
// because findSplice cannot prove a producer's single reader runs
// when a branch boundary sits between them. Slots written inside an
// arm are counted as written twice, so the write-once interface
// aliasing in argNode never fires for a maybe-written slot; that is
// the allocation cost docs/design/conditions.md predicts, and the
// if fixture benchmark measures it.

// hasIf reports an if statement in the program's top-level list. A
// nested if sits inside one, so the top level is enough.
func hasIf(stmts []vmStmt) bool {
	for i := range stmts {
		if stmts[i].ifs != nil {
			return true
		}
	}
	return false
}

// planIf is the structural plan for a program with an if.
func planIf(p *vmProgram) (*jitPlan, error) {
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
			// Only a name that already has a slot returns on this
			// tier, the same rule planInline applies.
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
		ps, err := planIfStmt(s)
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
	ifCounters(p, p.stmts, false, plan)
	return plan, nil
}

// planIfStmt converts one statement for the structural plan. It is
// planInline's per-statement mapping without the splice pass; an if
// travels whole, and its arms convert again when the node builder
// reaches them.
func planIfStmt(s *vmStmt) (plannedStmt, error) {
	out := -1
	if len(s.out) > 0 {
		out = s.out[0]
	}
	switch {
	case s.ifs != nil:
		return plannedStmt{ifs: s.ifs, out: -1}, nil
	case s.assign != nil:
		return plannedStmt{assign: s.assign, out: out}, nil
	case s.fieldSet != nil:
		return plannedStmt{fieldSet: s.fieldSet, out: -1}, nil
	case s.recv != nil:
		return plannedStmt{recv: s.recv, out: out}, nil
	case s.send != nil:
		return plannedStmt{send: s.send, out: -1}, nil
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

// ifCounters fills writes and live over the statement tree. inArm
// bumps every write by two: a slot assigned inside an arm is
// maybe-written, so it must never count as written exactly once,
// which is the condition the interface aliasing in argNode needs.
// The count is conservative on purpose; the price is a copy where a
// straight-line program would alias, never a wrong value.
func ifCounters(p *vmProgram, stmts []vmStmt, inArm bool, plan *jitPlan) {
	bump := 1
	if inArm {
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
		if s.fieldSet != nil {
			plan.live[s.fieldSet.base] = true
			// Writing a field of a struct held by value mutates the
			// slot an aliased interface may point at, so it counts as
			// a write like it does in planInline.
			if t := p.slotTypes[s.fieldSet.base]; t != nil && t.Kind() != reflect.Pointer {
				plan.writes[s.fieldSet.base] += bump
			}
		}
		if s.ifs != nil {
			ifCounters(p, s.ifs.then, true, plan)
			ifCounters(p, s.ifs.els, true, plan)
		}
	}
}

// ifNode compiles an if chain: the predicate as bool bits, each arm
// its own statement list.
func (c *jitCompiler) ifNode(n *vmIf, jp *jitProgram) (nodeE, error) {
	cond, err := c.predNode(n.pred)
	if err != nil {
		return nil, err
	}
	then, err := c.armNodes(n.then, jp)
	if err != nil {
		return nil, err
	}
	els, err := c.armNodes(n.els, jp)
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

// armNodes compiles one arm's statement list through the same
// stmtNode path the top level takes.
func (c *jitCompiler) armNodes(stmts []vmStmt, jp *jitProgram) ([]nodeE, error) {
	if len(stmts) == 0 {
		return nil, nil
	}
	out := make([]nodeE, 0, len(stmts))
	for i := range stmts {
		ps, err := planIfStmt(&stmts[i])
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

// condShapes extends the shape table with the predicate shapes the
// rung's conditions call, consulted by callNode after its own cases.
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

// cmpNode compiles a header comparison to bool bits. Both sides
// carry one static type, so one layout class closes the whole node:
// the operand loads and the compare are machine operations, and the
// only reflect a comparison can pay sits inside an operand call the
// shape table cannot express.
func (c *jitCompiler) cmpNode(cm *vmCmp) (nodeN, error) {
	cl := layoutOf(cm.typ)
	if cl == lBad {
		return nil, fmt.Errorf("a comparison over %s is not in the table", cm.typ)
	}
	x, err := c.cmpOperandNode(cm.lhs, cm.typ, cl)
	if err != nil {
		return nil, err
	}
	y, err := c.cmpOperandNode(cm.rhs, cm.typ, cl)
	if err != nil {
		return nil, err
	}
	switch {
	case cl == lStr:
		return cmpNodeOf(cm.op, x.S, y.S)
	case cl.float():
		return cmpNodeOf(cm.op, x.F, y.F)
	case cl == lBool:
		// The vm compiler admits only == and != for bool, and the
		// bits a nodeN carries are 0 or 1, so the integer compare is
		// the bool compare.
		return cmpNodeN(cm.op, cl, x.N, y.N)
	case cl.scalar():
		return cmpNodeN(cm.op, cl, x.N, y.N)
	}
	return nil, fmt.Errorf("a comparison over class %s is not in the table", cl)
}

// cmpOperandNode compiles one side of a comparison or an arithmetic
// node to the shared class. A call goes through exprNode so a
// predicate operand compiles exactly like a condition call does; an
// arithmetic subtree recurses through arithNode.
func (c *jitCompiler) cmpOperandNode(a *vmArg, t reflect.Type, cl layout) (node, error) {
	var n node
	var err error
	switch a.kind {
	case vaCall:
		n, err = c.exprNode(a.sub)
	case vaArith:
		n, err = c.arithNode(a)
	default:
		n, err = c.argNode(a, t, cl)
	}
	if err != nil {
		return node{}, err
	}
	if n.class != cl {
		return node{}, fmt.Errorf("a %s operand cannot compare as %s", n.class, cl)
	}
	return n, nil
}

// signN sign-extends the zero-extended bits a nodeN carries, so a
// signed class orders by value: int8(-1) travels as 0xFF and must
// compare below 0.
func signN(cl layout, v uint64) int64 {
	switch cl {
	case lI8:
		return int64(int8(uint8(v)))
	case lI16:
		return int64(int16(uint16(v)))
	case lI32:
		return int64(int32(uint32(v)))
	}
	return int64(v) // lI64
}

// signedClass reports a class whose ordering sign-extends.
func signedClass(cl layout) bool {
	return cl == lI8 || cl == lI16 || cl == lI32 || cl == lI64
}

// cmpNodeN builds the comparison over integer and bool bits. A
// signed class runs every operator through signN, which truncates
// to the class width and sign-extends: that canonicalizes the two
// bit conventions in play, the zero-extended loads and the
// sign-extended constant bits scalarBits produces, before any
// compare. Unsigned and bool bits are already canonical.
func cmpNodeN(op string, cl layout, x, y nodeN) (nodeN, error) {
	if signedClass(cl) {
		return cmpNodeSigned(op, cl, x, y)
	}
	var f func(a, b uint64) bool
	switch op {
	case "==":
		f = func(a, b uint64) bool { return a == b }
	case "!=":
		f = func(a, b uint64) bool { return a != b }
	case "<":
		f = func(a, b uint64) bool { return a < b }
	case "<=":
		f = func(a, b uint64) bool { return a <= b }
	case ">":
		f = func(a, b uint64) bool { return a > b }
	case ">=":
		f = func(a, b uint64) bool { return a >= b }
	default:
		return nil, fmt.Errorf("operator %s is not in the table", op)
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		a, err := x(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		b, err := y(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		if f(a, b) {
			return 1, nil
		}
		return 0, nil
	}, nil
}

// cmpNodeSigned is cmpNodeN's signed half.
func cmpNodeSigned(op string, cl layout, x, y nodeN) (nodeN, error) {
	var f func(a, b int64) bool
	switch op {
	case "==":
		f = func(a, b int64) bool { return a == b }
	case "!=":
		f = func(a, b int64) bool { return a != b }
	case "<":
		f = func(a, b int64) bool { return a < b }
	case "<=":
		f = func(a, b int64) bool { return a <= b }
	case ">":
		f = func(a, b int64) bool { return a > b }
	case ">=":
		f = func(a, b int64) bool { return a >= b }
	default:
		return nil, fmt.Errorf("operator %s is not in the table", op)
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		a, err := x(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		b, err := y(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		if f(signN(cl, a), signN(cl, b)) {
			return 1, nil
		}
		return 0, nil
	}, nil
}

// cmpNodeOf builds the comparison over the classes whose nodes carry
// their value directly: strings as nodeS, floats as nodeF. A float32
// travels as float64, an exact conversion, so one compare covers
// both widths.
func cmpNodeOf[T string | float64](op string, x, y func(unsafe.Pointer, context.Context, map[string]any, any) (T, error)) (nodeN, error) {
	var f func(a, b T) bool
	switch op {
	case "==":
		f = func(a, b T) bool { return a == b }
	case "!=":
		f = func(a, b T) bool { return a != b }
	case "<":
		f = func(a, b T) bool { return a < b }
	case "<=":
		f = func(a, b T) bool { return a <= b }
	case ">":
		f = func(a, b T) bool { return a > b }
	case ">=":
		f = func(a, b T) bool { return a >= b }
	default:
		return nil, fmt.Errorf("operator %s is not in the table", op)
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		a, err := x(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		b, err := y(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		if f(a, b) {
			return 1, nil
		}
		return 0, nil
	}, nil
}
