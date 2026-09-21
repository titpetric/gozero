package gozero

import (
	"reflect"
)

// The frame post-pass. Every call in a compiled program gets a
// disjoint window into one per-run argument frame, so one allocation
// covers them all and a nested call cannot overwrite the arguments
// its parent is still filling. The same walk records the slots a
// program takes the address of and pre-converts the interface
// arguments worth pre-converting, because all three want the same
// traversal of the statement tree.

// assignStmts walks a statement list for the frame post-pass. An if
// statement descends into its condition and both arms and a range
// into its bound and its body, so a call anywhere in the tree gets
// its window.
func (p *vmProgram) assignStmts(stmts []vmStmt) {
	for i := range stmts {
		s := &stmts[i]
		if s.call != nil {
			p.assignFrame(s.call)
		}
		if s.assign != nil {
			p.assignArg(s.assign)
		}
		if s.fieldSet != nil {
			p.assignArg(s.fieldSet.val)
		}
		if s.retArg != nil {
			p.assignArg(s.retArg)
		}
		if s.recv != nil {
			p.assignArg(s.recv.ch)
		}
		if s.send != nil {
			p.assignArg(s.send.ch)
			p.assignArg(s.send.val)
		}
		if s.ifs != nil {
			for _, a := range s.ifs.condArgs() {
				p.assignArg(a)
			}
			p.assignStmts(s.ifs.then)
			p.assignStmts(s.ifs.els)
		}
		if s.rng != nil {
			p.assignArg(s.rng.over)
			p.assignStmts(s.rng.body)
		}
	}
}

// assignFrame gives every call in the program a disjoint window into
// the per-run argument frame, so one allocation covers them all and a
// nested call cannot overwrite the arguments its parent is still
// filling.
func (p *vmProgram) assignFrame(c *vmCall) {
	c.off = p.frame
	p.frame += len(c.args)
	for _, a := range c.args {
		p.assignArg(a)
	}
}

// assignArg walks one argument for assignFrame: a nested call needs
// its own frame window whether it sits in an argument list, a struct
// element, or a field assignment's value.
func (p *vmProgram) assignArg(a *vmArg) {
	// An addressed argument pins the slot at its root: run stores the
	// slot's value through an addressable cell, and the step JIT
	// refuses to splice its producer or alias it behind an interface.
	if a.addrOf {
		root := a
		for root.kind == vaField {
			root = root.src
		}
		if root.kind == vaSlot {
			p.addrTaken[root.slot] = true
		}
	}
	for a.kind == vaField {
		a = a.src
	}
	switch a.kind {
	case vaCall:
		p.assignFrame(a.sub)
	case vaStruct:
		for i := range a.elems {
			p.assignArg(a.elems[i].val)
		}
	case vaStack, vaDest:
		// Only a non-empty interface is worth pre-converting: for
		// an empty one reflect packs an eface directly and never
		// reaches implements.
		if a.typ.Kind() == reflect.Interface && a.typ.NumMethod() > 0 {
			if conv, ok := ifaceConvs[a.typ]; ok {
				a.conv = conv
				a.iface = p.nifaces
				p.nifaces++
			}
		}
	}
}
