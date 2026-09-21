package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// The range loop on the reflect tier: for x := range xs over slices
// and arrays, and for i := range n over integers. The body is a
// nested statement list run by runStmts, the same function the
// program's own list and an if arm go through, so a body holds what a
// program holds minus return and var. Its exits are break, continue,
// an error, and the execution context, which is checked before every
// iteration: that check is what bounds a loop the way the statement
// count bounds a straight line.
//
// Scope is flat, as it is everywhere in this language. The loop
// variable is one program-level slot reused per iteration, and a name
// the body defines stays defined after the loop, holding its last
// value. Both diverge from Go's block scoping and both hold on both
// tiers: a binding that keeps a pointer to the loop variable observes
// the reuse, because the storage is the slot, not a fresh variable
// per iteration.

// vmRange is a compiled range loop. keySlot and valSlot are -1 when
// the name is blank or the form binds fewer than two.
type vmRange struct {
	over    *vmArg
	overInt bool // ranging an integer rather than a slice or array
	keySlot int
	valSlot int
	keyType reflect.Type
	elem    reflect.Type // element type, nil for an integer range
	body    []vmStmt
}

var intType = reflect.TypeFor[int]()

// compileRange compiles one range loop: the ranged expression, the
// key and value slots, and the body through compileStmts, the same
// per-list compiler the program and the if arms use. The branch
// counter is deliberately not raised: a := inside a loop body is
// allowed, and the name it declares outlives the loop, which is what
// flat scope means here.
func (pc *progCompiler) compileRange(rs *rangeStmt, dst *[]vmStmt) error {
	over, ot, err := pc.compileRangeOver(rs.over)
	if err != nil {
		return err
	}
	rng := &vmRange{over: over, keySlot: -1, valSlot: -1}
	var kt, vt reflect.Type
	switch ot.Kind() {
	case reflect.Slice, reflect.Array:
		kt, vt = intType, ot.Elem()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		// The go1.22 integer range: one variable, typed as the bound.
		if rs.val != "" && rs.val != "_" {
			return fmt.Errorf("compile: a range over %s permits one iteration variable", ot)
		}
		kt = ot
		rng.overInt = true
	default:
		return fmt.Errorf("compile: cannot range over %s; this language ranges slices, arrays and integers", ot)
	}
	if rs.key != "" && rs.key != "_" {
		if err := pc.checkName(rs.key); err != nil {
			return err
		}
		rng.keySlot = pc.newSlot(rs.key, kt)
		rng.keyType = kt
	}
	if rs.val != "" && rs.val != "_" {
		if err := pc.checkName(rs.val); err != nil {
			return err
		}
		rng.valSlot = pc.newSlot(rs.val, vt)
	}
	rng.elem = vt
	if err := pc.compileStmts(rs.body, &rng.body); err != nil {
		return err
	}
	*dst = append(*dst, vmStmt{rng: rng})
	return nil
}

// compileRangeOver resolves the ranged expression to an argument with
// a static type: an integer literal, a name the program bound, a
// field read off one, or a call. A stack name is rejected because its
// type is only known at execution.
func (pc *progCompiler) compileRangeOver(a arg) (*vmArg, reflect.Type, error) {
	if a.kind == argInt {
		v := reflect.ValueOf(a.i)
		return &vmArg{kind: vaConst, val: v, typ: v.Type(), iface: -1}, v.Type(), nil
	}
	va, t, err := pc.c.chanSource(pc.slots, pc.env, a)
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
	setKey := func(i int64) {
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
		switch _, err := p.runStmts(ctx, slots, frame, ifaces, stack, dest, r.body); err {
		case nil, errLoopContinue:
			return true, nil
		case errLoopBreak:
			return false, nil
		default:
			return false, err
		}
	}

	if r.overInt {
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
			setKey(int64(u))
			more, err := step()
			if err != nil || !more {
				return err
			}
		}
		return nil
	}
	for i := 0; i < over.Len(); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		setKey(int64(i))
		if r.valSlot >= 0 {
			// A copy, because an Index value is a view into the slice
			// and Go's range variable is not.
			valCell.Set(over.Index(i))
			slots[r.valSlot] = valCell
		}
		more, err := step()
		if err != nil || !more {
			return err
		}
	}
	return nil
}
