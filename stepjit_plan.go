package gozero

import (
	"fmt"
	"reflect"
)

// countStackReads tallies the stack reads dynamicNode would compile to
// a map lookup, walking the same edges countReads does plus the
// spliced producers, whose arguments compile too.
func (c *jitCompiler) countStackReads(plan *jitPlan) map[string]int {
	counts := map[string]int{}
	var walkCall func(*vmCall)
	walkArg := func(a *vmArg) {
		for a.kind == vaField {
			a = a.src
		}
		switch a.kind {
		case vaStack:
			if a.typ != nil {
				if cl := layoutOf(a.typ); cl == lIface || cl == lStr {
					counts[a.name]++
				}
			}
		case vaCall:
			walkCall(a.sub)
		case vaSlot:
			if sub := c.splices[a]; sub != nil {
				walkCall(sub)
			}
		}
	}
	walkCall = func(call *vmCall) {
		for _, a := range call.args {
			walkArg(a)
		}
	}
	for _, s := range plan.stmts {
		if s.call != nil {
			walkCall(s.call)
		}
		if s.fieldSet != nil {
			walkArg(s.fieldSet.val)
		}
	}
	return counts
}

// plannedStmt is one statement after inlining, with the slot its result
// is stored to.
type plannedStmt struct {
	call *vmCall
	out  int // frame slot, -1 to discard
	ret  bool

	// lit is a literal assignment, which has no call to compile.
	lit reflect.Value

	// fieldSet is a field assignment, compiled to a typed store.
	fieldSet *vmFieldSet
}

// jitPlan is everything planInline works out for the compiler.
type jitPlan struct {
	stmts   []plannedStmt
	live    map[int]bool
	writes  map[int]int
	splices map[*vmArg]*vmCall
	// retSlot is the slot a "return name;" reads, -1 when the program
	// returns through a trailing call or not at all.
	retSlot int
}

// planInline drops a statement whose single result is read exactly once
// by a later statement, splicing the call into the reader's argument
// tree. The value then travels as a closure's return value and needs no
// slot at all.
//
// The splice only happens when every argument the reader evaluates
// before it is free of side effects, which keeps the order of calls the
// source wrote. A receiver is argument zero and always qualifies, so a
// method chain written across statements collapses exactly as the same
// chain written on one line does.
func planInline(p *vmProgram) (*jitPlan, error) {
	retSlot := -1
	stmts := make([]plannedStmt, 0, len(p.stmts))
	for i := range p.stmts {
		s := &p.stmts[i]
		if s.retArg != nil {
			// Only a name that already has a slot returns on this
			// tier. A field read, a literal or a stack name in return
			// position is rare enough that the reflect evaluator keeps
			// it; what must not happen is the statement being skipped,
			// which would silently return nil where reflect returns
			// the value.
			if s.retArg.kind != vaSlot {
				return nil, fmt.Errorf("a returned expression is not in the table")
			}
			retSlot = s.retArg.slot
			continue
		}
		if s.fieldSet != nil {
			stmts = append(stmts, plannedStmt{fieldSet: s.fieldSet, out: -1})
			continue
		}
		if s.lit.IsValid() {
			out := -1
			if len(s.out) > 0 {
				out = s.out[0]
			}
			stmts = append(stmts, plannedStmt{lit: s.lit, out: out})
			continue
		}
		if s.call == nil {
			continue // a bare "return;" leaves the program without a value
		}
		if s.ret && i != len(p.stmts)-1 {
			return nil, fmt.Errorf("a return before the last statement is not a straight line")
		}
		out := -1
		if len(s.out) > 0 {
			out = s.out[0]
		}
		stmts = append(stmts, plannedStmt{call: s.call, out: out, ret: s.ret})
	}

	reads := map[int]int{}
	if retSlot >= 0 {
		reads[retSlot]++
	}
	for _, s := range stmts {
		if s.call != nil {
			countReads(reads, s.call)
		}
		if s.fieldSet != nil {
			reads[s.fieldSet.base]++
			if sub := s.fieldSet.val.sub; sub != nil {
				countReads(reads, sub)
			}
		}
	}

	splices := map[*vmArg]*vmCall{}
	for i := 0; i+1 < len(stmts); i++ {
		s := stmts[i]
		if s.call == nil || s.ret || s.out < 0 || s.call.nres != 1 || reads[s.out] != 1 {
			continue
		}
		if stmts[i+1].call == nil {
			continue // a literal assignment has no argument to splice into
		}
		// Only into the statement immediately after. Every statement
		// runs a call, so moving a producer past one would reorder two
		// calls the source wrote in the other order.
		arg := findSplice(stmts[i+1].call, s.out, splices)
		if arg == nil {
			continue
		}
		splices[arg] = s.call
		stmts = append(stmts[:i], stmts[i+1:]...)
		i--
	}

	live := map[int]bool{}
	writes := map[int]int{}
	if retSlot >= 0 {
		live[retSlot] = true
	}
	// A var declaration puts a name in scope whether or not anything
	// assigns it, so its slot is live from the start. The frame comes
	// back zeroed, which is exactly the zero value the declaration
	// promises, so there is nothing to run for it.
	for _, in := range p.inits {
		live[in.slot] = true
		writes[in.slot]++
	}
	for _, s := range stmts {
		if s.fieldSet != nil {
			live[s.fieldSet.base] = true
			// Writing a field of a struct held by value mutates the
			// slot an aliased interface may point at, so it counts as
			// a write and the aliasing falls back to a copy.
			if t := p.slotTypes[s.fieldSet.base]; t != nil && t.Kind() != reflect.Pointer {
				writes[s.fieldSet.base]++
			}
			continue
		}
		if s.out >= 0 {
			live[s.out] = true
			writes[s.out]++
		}
		if s.ret && s.call.nres > 0 && s.out < 0 {
			return nil, fmt.Errorf("a returned value needs a slot")
		}
	}
	return &jitPlan{stmts: stmts, live: live, writes: writes, splices: splices, retSlot: retSlot}, nil
}

// countReads tallies how many times each name is read, which decides
// whether its producer can be spliced into the reader and the slot
// dropped. It descends through a field, because req.Header is a read of
// req: missing those undercounts, and a name read once directly and
// once through a field would have its producer spliced away while the
// field read still pointed at the dropped slot.
func countReads(reads map[int]int, c *vmCall) {
	for _, a := range c.args {
		for a.kind == vaField {
			a = a.src
		}
		switch a.kind {
		case vaSlot:
			reads[a.slot]++
		case vaCall:
			countReads(reads, a.sub)
		}
	}
}

// findSplice returns the argument of c that reads slot and can take the
// producer in place, or nil. It refuses when anything c evaluates
// earlier has a side effect, which keeps the order of calls the source
// wrote; a receiver is argument zero and always qualifies.
func findSplice(c *vmCall, slot int, splices map[*vmArg]*vmCall) *vmArg {
	for i, a := range c.args {
		if a.kind == vaSlot && a.slot == slot && splices[a] == nil {
			for _, before := range c.args[:i] {
				if before.kind == vaCall || splices[before] != nil {
					return nil
				}
			}
			return a
		}
		if sub := subCall(splices, a); sub != nil {
			if found := findSplice(sub, slot, splices); found != nil {
				return found
			}
		}
	}
	return nil
}

// subCall is the call an argument evaluates, whether it was written
// nested or spliced in by planInline.
func subCall(splices map[*vmArg]*vmCall, a *vmArg) *vmCall {
	for a.kind == vaField {
		a = a.src
	}
	if a.kind == vaCall {
		return a.sub
	}
	return splices[a]
}
