package gozero

import (
	"reflect"
	"sync"
	"unsafe" // also required by go:linkname
)

// Argument pooling. The binding contract (see Bind) makes every
// call's arguments borrowed: valid for the duration of the call, with
// the callee copying anything it keeps. The memory behind them is
// therefore dead the moment the call returns. Three allocation kinds
// qualify, each with a fixed shape known at compile time:
//
//   - the variadic pack slice a packed call builds
//   - the heap cell a string boxes into for an interface parameter
//   - the block a composite literal in argument position fills
//
// Each pooled site gets one hidden unsafe.Pointer field in the
// frame. The node writes the block it took from the site's pool into
// that scratch field, run releases every site after the statements
// finish, and the release clears the block before returning it so a
// pooled block carries no references and comes back zeroed. The
// plan is a side table keyed by call and argument identity, so a
// site the node builder does not reach (a call that bridges, an
// argument that aliases instead of boxing) leaves its scratch unused,
// which costs one idle pointer field and nothing else.

// poolSite is one pooled allocation: the scratch field's offset and
// the release that clears and repools the block behind it.
type poolSite struct {
	off     uintptr
	release func(scratch unsafe.Pointer)
}

// sitePool is one site's pool plus the metadata its node and release
// need. field is the scratch field's index; the offset exists once
// the frame is laid out.
type sitePool struct {
	pool  *sync.Pool
	rt    unsafe.Pointer // rtype of the pooled block, for the typed clear
	field int
}

// take returns a cleared block: fresh from the allocator, or reused
// from the pool. Blocks are cleared on release, so both paths hand
// back zeroed memory.
func (s *sitePool) take() unsafe.Pointer {
	if v := s.pool.Get(); v != nil {
		return v.(unsafe.Pointer)
	}
	return unsafeNew(s.rt)
}

// releaseInto builds the poolSite for a scratch field: clear the
// block, repool it, zero the scratch.
func (s *sitePool) releaseInto(off uintptr) poolSite {
	return poolSite{off: off, release: func(scratch unsafe.Pointer) {
		p := *(*unsafe.Pointer)(scratch)
		if p == nil {
			return
		}
		typedmemclr(s.rt, p)
		s.pool.Put(p)
		*(*unsafe.Pointer)(scratch) = nil
	}}
}

// newPoolSite allocates the scratch field and the pool for one site
// of blocks typed t, and returns the accessor the node builder uses.
func (c *jitCompiler) newPoolSite(t reflect.Type) *sitePool {
	s := &sitePool{pool: &sync.Pool{}, rt: rtypePtr(t), field: len(c.types)}
	c.poolSites = append(c.poolSites, s)
	c.types = append(c.types, reflect.TypeFor[unsafe.Pointer]())
	return s
}

// planPools walks the planned statements and records every pooled
// site of every call: pack slices by call, literal blocks and
// certain string boxes by argument. The walk mirrors the
// node builders loosely on purpose: a site recorded here that the
// builder never uses is an idle scratch field, and a boxing site the
// plan misses allocates fresh, so a mismatch in either direction is
// unpooled, never unsound.
func (c *jitCompiler) planPools(plan *jitPlan) {
	var walkCall func(*vmCall)
	var walkArg func(a *vmArg, pt reflect.Type)
	walkArg = func(a *vmArg, pt reflect.Type) {
		if sub := subCall(c.splices, a); sub != nil {
			walkCall(sub)
		}
		if a.kind == vaStruct {
			for i := range a.elems {
				walkArg(a.elems[i].val, a.elems[i].val.typ)
			}
		}
	}
	markArg := func(a *vmArg, pt reflect.Type) {
		if pt.Kind() != reflect.Interface {
			return
		}
		switch {
		case a.kind == vaStruct:
			c.blockOf[a] = c.newPoolSite(a.styp)
		case c.argBoxesString(a):
			c.strboxOf[a] = c.newPoolSite(strType)
		}
	}
	walkCall = func(call *vmCall) {
		ft := call.fn.Type()
		fixed := ft.NumIn()
		if ft.IsVariadic() && !call.spread {
			fixed--
		}
		if ft.IsVariadic() && !call.spread {
			et := ft.In(ft.NumIn() - 1).Elem()
			if n := len(call.args) - fixed; n > 0 && (et == anyType || et.Kind() == reflect.String) {
				c.packOf[call] = c.newPoolSite(reflect.ArrayOf(n, et))
			}
		}
		for i, a := range call.args {
			pt := a.typ
			if i < fixed {
				pt = ft.In(i)
			} else if ft.IsVariadic() {
				pt = ft.In(ft.NumIn() - 1).Elem()
			}
			if a.kind == vaStruct && pt.Kind() == reflect.Pointer && a.addr {
				c.blockOf[a] = c.newPoolSite(a.styp)
			} else {
				markArg(a, pt)
			}
			walkArg(a, pt)
		}
	}
	// walkStmts covers an if's arms, whose statements are still
	// vmStmt: the same sites as the plannedStmt loop below, so a call
	// inside an arm pools its pack and boxes like one outside.
	var walkStmts func([]vmStmt)
	walkStmts = func(stmts []vmStmt) {
		for i := range stmts {
			s := &stmts[i]
			if s.call != nil {
				walkCall(s.call)
			}
			if s.assign != nil {
				walkArg(s.assign, s.assign.typ)
			}
			if s.fieldSet != nil {
				walkArg(s.fieldSet.val, s.fieldSet.val.typ)
			}
			if s.recv != nil {
				walkArg(s.recv.ch, s.recv.ch.typ)
			}
			if s.send != nil {
				walkArg(s.send.ch, s.send.ch.typ)
				walkArg(s.send.val, s.send.val.typ)
			}
			if s.ifs != nil {
				for _, a := range s.ifs.condArgs() {
					walkArg(a, a.typ)
				}
				walkStmts(s.ifs.then)
				walkStmts(s.ifs.els)
			}
		}
	}
	for _, s := range plan.stmts {
		if s.call != nil {
			walkCall(s.call)
		}
		if s.assign != nil {
			walkArg(s.assign, s.assign.typ)
		}
		if s.fieldSet != nil {
			walkArg(s.fieldSet.val, s.fieldSet.val.typ)
		}
		if s.recv != nil {
			walkArg(s.recv.ch, s.recv.ch.typ)
		}
		if s.send != nil {
			walkArg(s.send.ch, s.send.ch.typ)
			walkArg(s.send.val, s.send.val.typ)
		}
		if s.ifs != nil {
			for _, a := range s.ifs.condArgs() {
				walkArg(a, a.typ)
			}
			walkStmts(s.ifs.then)
			walkStmts(s.ifs.els)
		}
	}
}

// argBoxesString reports whether an argument certainly produces a
// string value that toIface would box for an interface parameter. A
// write-once string slot aliases the frame instead and needs no box;
// the cases below copy.
func (c *jitCompiler) argBoxesString(a *vmArg) bool {
	if producer := c.splices[a]; producer != nil {
		rt := callResultType(producer, 0)
		return rt != nil && rt.Kind() == reflect.String
	}
	switch a.kind {
	case vaCall:
		rt := callResultType(a.sub, 0)
		return rt != nil && rt.Kind() == reflect.String
	case vaField:
		return a.typ != nil && a.typ.Kind() == reflect.String
	}
	return false
}

var anyType = reflect.TypeFor[any]()

// armStrBox hands the argument's string-box site to the next toIface
// call, which is made immediately by the caller. See pendingStrBox.
func (c *jitCompiler) armStrBox(a *vmArg) {
	if site, ok := c.strboxOf[a]; ok {
		c.pendingStrBox = site
	}
}
