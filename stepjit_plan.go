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
	var walkArg func(*vmArg)
	walkArg = func(a *vmArg) {
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
		case vaStruct:
			for i := range a.elems {
				walkArg(a.elems[i].val)
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
		if s.assign != nil {
			walkArg(s.assign)
		}
		if s.fieldSet != nil {
			walkArg(s.fieldSet.val)
		}
	}
	return counts
}

// plannedStmt is one statement after inlining, with the slot that
// receives its result.
type plannedStmt struct {
	call *vmCall
	out  int // frame slot, -1 to discard
	ret  bool

	// lit is a literal assignment, which has no call to compile.
	lit reflect.Value

	// assign is a composite literal assignment, built per evaluation.
	assign *vmArg

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
	// inits are the program's slot initialisers, passed through so
	// the compiler can emit a prologue for the seeded ones.
	inits []slotInit
}

// planInline drops a statement whose single result a later statement
// reads exactly once, splicing the call into the reader's argument
// tree. The value then travels as a closure's return value and needs no
// slot at all.
//
// The splice only happens when every argument the reader evaluates
// before the spliced one has no side effects, which keeps the order of
// calls the source wrote. Because a receiver is argument zero and
// always qualifies, a method chain written across statements collapses
// exactly as the same chain written on one line does.
func planInline(p *vmProgram) (*jitPlan, error) {
	retSlot := -1
	stmts := make([]plannedStmt, 0, len(p.stmts))
	for i := range p.stmts {
		s := &p.stmts[i]
		if s.assign != nil {
			out := -1
			if len(s.out) > 0 {
				out = s.out[0]
			}
			stmts = append(stmts, plannedStmt{assign: s.assign, out: out})
			continue
		}
		if s.retArg != nil {
			// Only a name that already has a slot returns on this
			// tier. A field read, a literal or a stack name in return
			// position is rare enough that the reflect evaluator keeps
			// it; the JIT must never skip the statement, since that
			// would silently return nil where reflect returns the
			// value.
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
		if s.varSet != nil {
			// A write to a value binding has no node yet. Refusing the
			// program sends it to the reflect evaluator whole, which
			// is the fallback; falling through here would drop the
			// statement and lose the write silently.
			return nil, fmt.Errorf("a write to a value binding is not in the table")
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
		if s.assign != nil {
			countArgReads(reads, s.assign)
		}
		if s.fieldSet != nil {
			reads[s.fieldSet.base]++
			countArgReads(reads, s.fieldSet.val)
		}
	}

	splices := map[*vmArg]*vmCall{}
	for i := 0; i+1 < len(stmts); i++ {
		s := stmts[i]
		if s.call == nil || s.ret || s.out < 0 || s.call.nres != 1 || reads[s.out] != 1 {
			continue
		}
		// A slot whose address the program takes must exist as
		// storage: splicing its producer would take the address of a
		// value travelling as a closure return.
		if p.addrTaken[s.out] {
			continue
		}
		if stmts[i+1].call == nil {
			continue // a literal assignment has no argument to splice into
		}
		// Only into the statement immediately after. Because every
		// statement runs a call, moving a producer past one would
		// reorder two calls the source wrote in the other order.
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
	// Because a var declaration puts a name in scope even when no
	// statement assigns it, its slot is live from the start. The frame
	// starts zeroed, matching the zero value the declaration promises,
	// which leaves the prologue no statement to emit for it.
	for _, in := range p.inits {
		live[in.slot] = true
		writes[in.slot]++
	}
	for _, s := range stmts {
		if s.fieldSet != nil {
			live[s.fieldSet.base] = true
			// Writing a field of a struct held by value mutates the
			// slot an aliased interface may point at. It counts as a
			// write and the aliasing falls back to a copy.
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
	return &jitPlan{stmts: stmts, live: live, writes: writes, splices: splices, retSlot: retSlot, inits: p.inits}, nil
}

// countReads tallies the reads of each name, which decides whether
// planInline can splice its producer into the reader and drop the
// slot. It descends through a field, because req.Header is a read of
// req: missing those undercounts, and a name read once directly and
// once through a field would lose its producer to a splice while the
// field read still pointed at the dropped slot.
func countReads(reads map[int]int, c *vmCall) {
	for _, a := range c.args {
		countArgReads(reads, a)
	}
}

// countArgReads walks one argument for countReads. Because a composite
// literal's elements read names like any argument does, planInline
// must keep a producer while an element still points at its slot.
func countArgReads(reads map[int]int, a *vmArg) {
	for a.kind == vaField {
		a = a.src
	}
	switch a.kind {
	case vaSlot:
		reads[a.slot]++
	case vaCall:
		countReads(reads, a.sub)
	case vaStruct:
		for i := range a.elems {
			countArgReads(reads, a.elems[i].val)
		}
	}
}

// findSplice returns the argument of c that reads slot and can take the
// producer in place, or nil. It refuses when anything c evaluates
// earlier has a side effect, which keeps the order of calls the source
// wrote; a receiver is argument zero and always qualifies.
func findSplice(c *vmCall, slot int, splices map[*vmArg]*vmCall) *vmArg {
	for i, a := range c.args {
		if a.kind == vaSlot && !a.addrOf && a.slot == slot && splices[a] == nil {
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

// subCall returns the call an argument evaluates, whether the source
// nests it or planInline splices it in.
func subCall(splices map[*vmArg]*vmCall, a *vmArg) *vmCall {
	for a.kind == vaField {
		a = a.src
	}
	if a.kind == vaCall {
		return a.sub
	}
	return splices[a]
}
