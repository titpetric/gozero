package gozero

import (
	"context"
	"reflect"
)

// Control flow on the reflect tier. A block is a statement list; if
// and for run blocks under child scopes the compiler resolved, and
// break, continue and return travel outward as signals rather than
// errors, so a binding can never forge one.

// ctlSig is what a block's execution reports upward.
type ctlSig int

const (
	sigNone ctlSig = iota
	sigBreak
	sigContinue
	sigReturn
)

// vmBlock is a compiled statement list.
type vmBlock struct {
	stmts []vmStmt
}

// vmIf is a compiled if chain: else-if nests as an els block whose
// single statement is another if.
type vmIf struct {
	cond *vmArg
	then vmBlock
	els  *vmBlock
}

// vmFor is any of the three loop forms; cond nil loops forever, init
// and post are at most one statement each, held as slices so the
// block runner executes them.
type vmFor struct {
	init []vmStmt
	cond *vmArg
	post []vmStmt
	body vmBlock
}

// vmRange is a compiled range loop; slots are -1 when blank. The
// ranged kind is fixed at compile time.
type vmRange struct {
	over    *vmArg
	keySlot int
	valSlot int
	body    vmBlock
}

// runBlock executes one statement list, reporting how it ended.
func (p *vmProgram) runBlock(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any, defers *deferStack, stmts []vmStmt) (ctlSig, any, error) {
	for i := range stmts {
		s := &stmts[i]
		if s.lit.IsValid() {
			slots[s.out[0]] = p.addrCell(s.lit, s.out[0])
			continue
		}
		if s.init != nil {
			// A block-scoped var re-zeroes on every pass, in a fresh
			// cell so its fields stay settable.
			slots[s.init.slot] = reflect.New(s.init.zero.Type()).Elem()
			continue
		}
		if s.assign != nil {
			v, err := s.assign.get(ctx, slots, frame, ifaces, stack, dest)
			if err != nil {
				return sigNone, nil, err
			}
			if slot := s.out[0]; slot >= 0 {
				slots[slot] = p.addrCell(v, slot)
			}
			continue
		}
		if s.fieldSet != nil {
			if err := s.fieldSet.apply(ctx, slots, frame, ifaces, stack, dest); err != nil {
				return sigNone, nil, err
			}
			continue
		}
		if s.recv != nil {
			v, err := s.recv.exec(ctx, slots, frame, ifaces, stack, dest)
			if err != nil {
				return sigNone, nil, err
			}
			if len(s.out) > 0 {
				slots[s.out[0]] = p.addrCell(v, s.out[0])
			}
			continue
		}
		if s.send != nil {
			if err := s.send.exec(ctx, slots, frame, ifaces, stack, dest); err != nil {
				return sigNone, nil, err
			}
			continue
		}
		if s.ifs != nil {
			sig, v, err := p.runIf(ctx, slots, frame, ifaces, stack, dest, defers, s.ifs)
			if err != nil || sig != sigNone {
				return sig, v, err
			}
			continue
		}
		if s.loop != nil {
			sig, v, err := p.runFor(ctx, slots, frame, ifaces, stack, dest, defers, s.loop)
			if err != nil || sig != sigNone {
				return sig, v, err
			}
			continue
		}
		if s.rng != nil {
			sig, v, err := p.runRange(ctx, slots, frame, ifaces, stack, dest, defers, s.rng)
			if err != nil || sig != sigNone {
				return sig, v, err
			}
			continue
		}
		if s.deferCall != nil {
			// Arguments evaluate now, the call waits: Go's rule.
			args := make([]reflect.Value, len(s.deferCall.args))
			for j, a := range s.deferCall.args {
				v, err := a.get(ctx, slots, frame, ifaces, stack, dest)
				if err != nil {
					return sigNone, nil, err
				}
				args[j] = v
			}
			defers.push(args, s.deferCall)
			continue
		}
		if s.brk {
			return sigBreak, nil, nil
		}
		if s.cont {
			return sigContinue, nil, nil
		}
		if s.retArg != nil {
			v, err := s.retArg.get(ctx, slots, frame, ifaces, stack, dest)
			if err != nil {
				return sigNone, nil, err
			}
			if !v.IsValid() {
				return sigReturn, nil, nil
			}
			return sigReturn, v.Interface(), nil
		}
		if s.call == nil {
			// A bare "return;".
			return sigReturn, nil, nil
		}
		out, err := s.call.invoke(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return sigNone, nil, err
		}
		n := 0
		for j := range out {
			if j == s.call.errIdx && !s.call.bindErr {
				continue
			}
			if n < len(s.out) {
				if slot := s.out[n]; slot >= 0 {
					slots[slot] = p.addrCell(out[j], slot)
				}
			}
			n++
		}
		if s.ret {
			if s.call.nres == 0 {
				return sigReturn, nil, nil
			}
			return sigReturn, firstNonErr(out, s.call.errIdx).Interface(), nil
		}
	}
	return sigNone, nil, nil
}

func (p *vmProgram) runIf(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any, defers *deferStack, n *vmIf) (ctlSig, any, error) {
	cv, err := n.cond.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return sigNone, nil, err
	}
	blk := &n.then
	if !cv.Bool() {
		if n.els == nil {
			return sigNone, nil, nil
		}
		blk = n.els
	}
	return p.runBlock(ctx, slots, frame, ifaces, stack, dest, defers, blk.stmts)
}

func (p *vmProgram) runFor(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any, defers *deferStack, l *vmFor) (ctlSig, any, error) {
	if len(l.init) > 0 {
		if sig, v, err := p.runBlock(ctx, slots, frame, ifaces, stack, dest, defers, l.init); err != nil || sig == sigReturn {
			return sig, v, err
		}
	}
	for {
		// The context bounds a loop the way it bounds a blocked
		// channel operation: a cancelled run does not spin on.
		if err := ctx.Err(); err != nil {
			return sigNone, nil, err
		}
		if l.cond != nil {
			cv, err := l.cond.get(ctx, slots, frame, ifaces, stack, dest)
			if err != nil {
				return sigNone, nil, err
			}
			if !cv.Bool() {
				return sigNone, nil, nil
			}
		}
		sig, v, err := p.runBlock(ctx, slots, frame, ifaces, stack, dest, defers, l.body.stmts)
		if err != nil {
			return sigNone, nil, err
		}
		if sig == sigReturn {
			return sigReturn, v, nil
		}
		if sig == sigBreak {
			return sigNone, nil, nil
		}
		if len(l.post) > 0 {
			if sig, v, err := p.runBlock(ctx, slots, frame, ifaces, stack, dest, defers, l.post); err != nil || sig == sigReturn {
				return sig, v, err
			}
		}
	}
}

func (p *vmProgram) runRange(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any, defers *deferStack, r *vmRange) (ctlSig, any, error) {
	over, err := r.over.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return sigNone, nil, err
	}
	setKey := func(v reflect.Value) {
		if r.keySlot >= 0 {
			slots[r.keySlot] = p.addrCell(v, r.keySlot)
		}
	}
	setVal := func(v reflect.Value) {
		if r.valSlot >= 0 {
			// A copy, because an Index value is a view into the slice
			// and Go's range variable is not.
			cell := reflect.New(v.Type()).Elem()
			cell.Set(v)
			slots[r.valSlot] = p.addrCell(cell, r.valSlot)
		}
	}
	run := func(key, val reflect.Value) (bool, ctlSig, any, error) {
		if err := ctx.Err(); err != nil {
			return false, sigNone, nil, err
		}
		setKey(key)
		setVal(val)
		sig, v, err := p.runBlock(ctx, slots, frame, ifaces, stack, dest, defers, r.body.stmts)
		if err != nil || sig == sigReturn {
			return false, sig, v, err
		}
		if sig == sigBreak {
			return false, sigNone, nil, nil
		}
		return true, sigNone, nil, nil
	}
	switch over.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < over.Len(); i++ {
			more, sig, v, err := run(reflect.ValueOf(i), over.Index(i))
			if !more {
				return sig, v, err
			}
		}
	case reflect.String:
		for i, rn := range over.String() {
			more, sig, v, err := run(reflect.ValueOf(i), reflect.ValueOf(rn))
			if !more {
				return sig, v, err
			}
		}
	case reflect.Map:
		iter := over.MapRange()
		for iter.Next() {
			more, sig, v, err := run(iter.Key(), iter.Value())
			if !more {
				return sig, v, err
			}
		}
	}
	return sigNone, nil, nil
}

// deferStack holds the calls a run deferred, executed LIFO when the
// run exits, whatever way it exits.
type deferStack struct {
	entries []deferredEntry
}

type deferredEntry struct {
	call *vmCall
	args []reflect.Value
}

func (d *deferStack) push(args []reflect.Value, call *vmCall) {
	d.entries = append(d.entries, deferredEntry{call: call, args: args})
}

// runAll executes the stack. A deferred call's trailing error is
// reported only through the first one seen, and only the caller
// decides whether it stands in for a nil program error; a panic
// propagates, as it does from a Go defer.
func (d *deferStack) runAll() error {
	var first error
	for i := len(d.entries) - 1; i >= 0; i-- {
		e := d.entries[i]
		var out []reflect.Value
		if e.call.spread {
			out = e.call.fn.CallSlice(e.args)
		} else {
			out = e.call.fn.Call(e.args)
		}
		if e.call.errIdx >= 0 {
			if ev := out[e.call.errIdx]; !ev.IsNil() && first == nil {
				first = ev.Interface().(error)
			}
		}
	}
	return first
}
