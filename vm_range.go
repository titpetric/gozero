package gozero

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
)

// The range loop on the reflect tier: slices, arrays, integers,
// strings by rune, maps, channels until close, and iterator funcs of
// the iter.Seq and iter.Seq2 shapes. The body is a nested statement
// list whose exits are an error, the execution context, and the two
// loop signals below; return and var are still rejected when the
// program parses.
//
// Scope is flat, as it is everywhere in this language. The loop
// variable is one program-level slot reused per iteration, and a name
// the body defines stays defined after the loop, holding its last
// value. Both diverge from Go's block scoping and both hold on both
// tiers: a binding that keeps a pointer to the loop variable observes
// the reuse, because the storage is the slot, not a fresh variable
// per iteration.

// errLoopBreak and errLoopContinue are the out-of-band control
// signals of break and continue. They travel the error return every
// statement already has, on both tiers: a body statement raises one,
// the innermost enclosing loop consumes it, and nothing else sees it.
// The parser only admits break and continue inside a range body, so a
// loop always encloses the raise and neither value can reach a
// program's caller; both are unexported, so a binding cannot forge
// one.
var (
	errLoopBreak    = errors.New("break outside a loop")
	errLoopContinue = errors.New("continue outside a loop")
)

// rangeKind is what a loop iterates, fixed at compile time.
type rangeKind int

const (
	rangeSlice  rangeKind = iota // a slice or an array, by index
	rangeInt                     // the go1.22 integer range
	rangeString                  // by rune, with the byte index as key
	rangeMap                     // by key and value, in map order
	rangeChan                    // the element, until the channel closes
	rangeFunc                    // an iter.Seq: func(func(V) bool)
	rangeFunc2                   // an iter.Seq2: func(func(K, V) bool)
)

// vmRange is a compiled range loop. keySlot and valSlot are -1 when
// the name is blank or the form binds fewer than two. keyType and
// elem are the static types of the first and second iteration values;
// elem is nil for a form that yields one.
type vmRange struct {
	kind    rangeKind
	over    *vmArg
	keySlot int
	valSlot int
	keyType reflect.Type
	elem    reflect.Type
	body    []vmStmt
}

var (
	intType  = reflect.TypeFor[int]()
	runeType = reflect.TypeFor[rune]()
)

// seqShape reports the yield parameter types of an iterator func
// type, Go's iter.Seq and iter.Seq2 shapes: func(func(V) bool) or
// func(func(K, V) bool). v is nil for the one-value form.
func seqShape(t reflect.Type) (k, v reflect.Type, ok bool) {
	if t.NumIn() != 1 || t.NumOut() != 0 || t.IsVariadic() {
		return nil, nil, false
	}
	y := t.In(0)
	if y.Kind() != reflect.Func || y.IsVariadic() || y.NumOut() != 1 || y.Out(0).Kind() != reflect.Bool {
		return nil, nil, false
	}
	switch y.NumIn() {
	case 1:
		return y.In(0), nil, true
	case 2:
		return y.In(0), y.In(1), true
	}
	return nil, nil, false
}

// compileRange compiles one range loop: the ranged expression, the
// key and value slots, and the body through the same per-statement
// compiler the program uses, so a body holds what a program holds.
// newSlot, checkName and compileStmt are compileProgram's own,
// closing over its slot table.
func (c *Compiler) compileRange(slots map[string]int, env map[string]reflect.Type, rs *rangeStmt, newSlot func(string, reflect.Type) int, checkName func(string) error, compileStmt func(stmt, *[]vmStmt) error) (*vmRange, error) {
	over, ot, err := c.compileRangeOver(slots, env, rs.over)
	if err != nil {
		return nil, err
	}
	rng := &vmRange{over: over, keySlot: -1, valSlot: -1}
	oneVar := func() error {
		if rs.val != "" && rs.val != "_" {
			return fmt.Errorf("compile: a range over %s permits one iteration variable", ot)
		}
		return nil
	}
	var kt, vt reflect.Type
	switch ot.Kind() {
	case reflect.Slice, reflect.Array:
		rng.kind = rangeSlice
		kt, vt = intType, ot.Elem()
	case reflect.String:
		rng.kind = rangeString
		kt, vt = intType, runeType
	case reflect.Map:
		rng.kind = rangeMap
		kt, vt = ot.Key(), ot.Elem()
	case reflect.Chan:
		if ot.ChanDir() == reflect.SendDir {
			return nil, fmt.Errorf("compile: cannot range over the send-only %s", ot)
		}
		if err := oneVar(); err != nil {
			return nil, err
		}
		rng.kind = rangeChan
		kt = ot.Elem()
	case reflect.Func:
		yk, yv, ok := seqShape(ot)
		if !ok {
			return nil, fmt.Errorf("compile: cannot range over %s; a range func is func(func(V) bool) or func(func(K, V) bool)", ot)
		}
		if yv == nil {
			if err := oneVar(); err != nil {
				return nil, err
			}
			rng.kind = rangeFunc
			kt = yk
		} else {
			rng.kind = rangeFunc2
			kt, vt = yk, yv
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		// The go1.22 integer range: one variable, typed as the bound.
		if err := oneVar(); err != nil {
			return nil, err
		}
		rng.kind = rangeInt
		kt = ot
	default:
		return nil, fmt.Errorf("compile: cannot range over %s", ot)
	}
	if rs.key != "" && rs.key != "_" {
		if err := checkName(rs.key); err != nil {
			return nil, err
		}
		rng.keySlot = newSlot(rs.key, kt)
	}
	if rs.val != "" && rs.val != "_" {
		if err := checkName(rs.val); err != nil {
			return nil, err
		}
		rng.valSlot = newSlot(rs.val, vt)
	}
	// The types stay even when the names are blank: a func range needs
	// the yield parameter shapes to cast its closure either way.
	rng.keyType, rng.elem = kt, vt
	for _, bs := range rs.body {
		if err := compileStmt(bs, &rng.body); err != nil {
			return nil, err
		}
	}
	return rng, nil
}

// compileRangeOver resolves the ranged expression to an argument with
// a static type: a literal, a name the program bound, a field read
// off one, or a call. A stack name is rejected because its type is
// only known at execution.
func (c *Compiler) compileRangeOver(slots map[string]int, env map[string]reflect.Type, a arg) (*vmArg, reflect.Type, error) {
	if a.kind == argInt || a.kind == argString {
		v := reflect.ValueOf(a.i)
		if a.kind == argString {
			v = reflect.ValueOf(a.str)
		}
		return &vmArg{kind: vaConst, val: v, typ: v.Type(), iface: -1}, v.Type(), nil
	}
	va, t, err := c.sourceArg(slots, env, a, "range bound")
	if err != nil {
		return nil, nil, fmt.Errorf("range: %w", err)
	}
	return va, t, nil
}

// runRange executes one loop. The key and value cells are allocated
// once and reused: the slot is the variable, on this tier as on the
// direct one. step runs one iteration's body and folds the loop
// signals: continue ends the iteration, break ends the loop, any
// other error ends the program.
func (p *vmProgram) runRange(ctx context.Context, r *vmRange, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) error {
	over, err := r.over.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return err
	}
	var keyCell, valCell reflect.Value
	if r.keySlot >= 0 {
		keyCell = reflect.New(r.keyType).Elem()
	}
	if r.valSlot >= 0 {
		valCell = reflect.New(r.elem).Elem()
	}
	setNum := func(i int64) {
		if r.keySlot < 0 {
			return
		}
		switch r.keyType.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			keyCell.SetUint(uint64(i))
		default:
			keyCell.SetInt(i)
		}
		slots[r.keySlot] = keyCell
	}
	step := func() (bool, error) {
		switch err := p.runBody(ctx, r.body, slots, frame, ifaces, stack, dest); err {
		case nil, errLoopContinue:
			return true, nil
		case errLoopBreak:
			return false, nil
		default:
			return false, err
		}
	}

	switch r.kind {
	case rangeInt:
		// The count travels as uint64 so the full unsigned domain
		// iterates; a negative signed bound runs zero times, as in Go.
		var count uint64
		switch over.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			count = over.Uint()
		default:
			if v := over.Int(); v > 0 {
				count = uint64(v)
			}
		}
		for u := uint64(0); u < count; u++ {
			// The context bounds every iteration, so a cancelled run
			// ends with ctx.Err() instead of running out the data.
			if err := ctx.Err(); err != nil {
				return err
			}
			setNum(int64(u))
			more, err := step()
			if err != nil || !more {
				return err
			}
		}
		return nil

	case rangeSlice:
		for i := 0; i < over.Len(); i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			setNum(int64(i))
			if r.valSlot >= 0 {
				// A copy, because an Index value is a view into the
				// slice and Go's range variable is not.
				valCell.Set(over.Index(i))
				slots[r.valSlot] = valCell
			}
			more, err := step()
			if err != nil || !more {
				return err
			}
		}
		return nil

	case rangeString:
		for i, rn := range over.String() {
			if err := ctx.Err(); err != nil {
				return err
			}
			setNum(int64(i))
			if r.valSlot >= 0 {
				valCell.SetInt(int64(rn))
				slots[r.valSlot] = valCell
			}
			more, err := step()
			if err != nil || !more {
				return err
			}
		}
		return nil

	case rangeMap:
		it := over.MapRange()
		for it.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			if r.keySlot >= 0 {
				keyCell.SetIterKey(it)
				slots[r.keySlot] = keyCell
			}
			if r.valSlot >= 0 {
				valCell.SetIterValue(it)
				slots[r.valSlot] = valCell
			}
			more, err := step()
			if err != nil || !more {
				return err
			}
		}
		return nil

	case rangeChan:
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			// The same armed receive a receive statement runs; its
			// io.EOF, the implicit ok, is the end of the loop here
			// rather than the end of the program.
			v, err := chanRecv(ctx, over)
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if r.keySlot >= 0 {
				keyCell.Set(v)
				slots[r.keySlot] = keyCell
			}
			more, err := step()
			if err != nil || !more {
				return err
			}
		}

	case rangeFunc:
		var out error
		for v := range over.Seq() {
			if err := ctx.Err(); err != nil {
				out = err
				break
			}
			if r.keySlot >= 0 {
				keyCell.Set(v)
				slots[r.keySlot] = keyCell
			}
			more, err := step()
			if err != nil {
				out = err
				break
			}
			if !more {
				break
			}
		}
		return out

	case rangeFunc2:
		var out error
		for k, v := range over.Seq2() {
			if err := ctx.Err(); err != nil {
				out = err
				break
			}
			if r.keySlot >= 0 {
				keyCell.Set(k)
				slots[r.keySlot] = keyCell
			}
			if r.valSlot >= 0 {
				valCell.Set(v)
				slots[r.valSlot] = valCell
			}
			more, err := step()
			if err != nil {
				out = err
				break
			}
			if !more {
				break
			}
		}
		return out
	}
	return fmt.Errorf("exec: range kind %d cannot run", r.kind)
}

// runBody executes a loop body: the statement kinds run does, minus
// return, which cannot stand inside a body, plus the two loop
// signals.
func (p *vmProgram) runBody(ctx context.Context, stmts []vmStmt, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) error {
	for i := range stmts {
		s := &stmts[i]
		if s.brk {
			return errLoopBreak
		}
		if s.cont {
			return errLoopContinue
		}
		if s.lit.IsValid() {
			slots[s.out[0]] = p.addrCell(s.lit, s.out[0])
			continue
		}
		if s.assign != nil {
			v, err := s.assign.get(ctx, slots, frame, ifaces, stack, dest)
			if err != nil {
				return err
			}
			slots[s.out[0]] = v
			continue
		}
		if s.fieldSet != nil {
			if err := s.fieldSet.apply(ctx, slots, frame, ifaces, stack, dest); err != nil {
				return err
			}
			continue
		}
		if s.recv != nil {
			v, err := s.recv.exec(ctx, slots, frame, ifaces, stack, dest)
			if err != nil {
				return err
			}
			if len(s.out) > 0 {
				slots[s.out[0]] = p.addrCell(v, s.out[0])
			}
			continue
		}
		if s.send != nil {
			if err := s.send.exec(ctx, slots, frame, ifaces, stack, dest); err != nil {
				return err
			}
			continue
		}
		if s.rng != nil {
			if err := p.runRange(ctx, s.rng, slots, frame, ifaces, stack, dest); err != nil {
				return err
			}
			continue
		}
		if s.fors != nil {
			if err := p.runFor(ctx, s.fors, slots, frame, ifaces, stack, dest); err != nil {
				return err
			}
			continue
		}
		if s.call == nil || s.ret {
			return fmt.Errorf("exec: a statement of this kind cannot stand inside a loop body")
		}
		out, err := s.call.invoke(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return err
		}
		n := 0
		for j := range out {
			if j == s.call.errIdx {
				continue
			}
			if n < len(s.out) {
				slots[s.out[n]] = p.addrCell(out[j], s.out[n])
			}
			n++
		}
	}
	return nil
}
