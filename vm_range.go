package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// The range loop on the reflect tier: for x := range xs over slices
// and arrays, and for i := range n over integers. The body is a
// nested statement list with no exits of its own: break, continue and
// return are rejected when the program parses, so a body runs every
// statement of every iteration and the only ways out are an error and
// the execution context, which is checked before each iteration.
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
	var kt, vt reflect.Type
	switch ot.Kind() {
	case reflect.Slice, reflect.Array:
		kt, vt = intType, ot.Elem()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		// The go1.22 integer range: one variable, typed as the bound.
		if rs.val != "" && rs.val != "_" {
			return nil, fmt.Errorf("compile: a range over %s permits one iteration variable", ot)
		}
		kt = ot
		rng.overInt = true
	default:
		return nil, fmt.Errorf("compile: cannot range over %s; this rung ranges slices, arrays and integers", ot)
	}
	if rs.key != "" && rs.key != "_" {
		if err := checkName(rs.key); err != nil {
			return nil, err
		}
		rng.keySlot = newSlot(rs.key, kt)
		rng.keyType = kt
	}
	if rs.val != "" && rs.val != "_" {
		if err := checkName(rs.val); err != nil {
			return nil, err
		}
		rng.valSlot = newSlot(rs.val, vt)
	}
	rng.elem = vt
	for _, bs := range rs.body {
		if err := compileStmt(bs, &rng.body); err != nil {
			return nil, err
		}
	}
	return rng, nil
}

// compileRangeOver resolves the ranged expression to an argument with
// a static type: an integer literal, a name the program bound, a
// field read off one, or a call. A stack name is rejected because its
// type is only known at execution.
func (c *Compiler) compileRangeOver(slots map[string]int, env map[string]reflect.Type, a arg) (*vmArg, reflect.Type, error) {
	if a.kind == argInt {
		v := reflect.ValueOf(a.i)
		return &vmArg{kind: vaConst, val: v, typ: v.Type(), iface: -1}, v.Type(), nil
	}
	va, t, err := c.chanSource(slots, env, a)
	if err != nil {
		return nil, nil, fmt.Errorf("range: %w", err)
	}
	return va, t, nil
}

// runRange executes one loop. The key and value cells are allocated
// once and reused: the slot is the variable, on this tier as on the
// direct one.
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
	step := func(i int64) error {
		// The context bounds every iteration, so a cancelled run ends
		// with ctx.Err() instead of running out the data.
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.keySlot >= 0 {
			switch r.keyType.Kind() {
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				keyCell.SetUint(uint64(i))
			default:
				keyCell.SetInt(i)
			}
			slots[r.keySlot] = keyCell
		}
		if r.valSlot >= 0 {
			// A copy, because an Index value is a view into the slice
			// and Go's range variable is not.
			valCell.Set(over.Index(int(i)))
			slots[r.valSlot] = valCell
		}
		return p.runBody(ctx, r.body, slots, frame, ifaces, stack, dest)
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
			if err := step(int64(u)); err != nil {
				return err
			}
		}
		return nil
	}
	for i := 0; i < over.Len(); i++ {
		if err := step(int64(i)); err != nil {
			return err
		}
	}
	return nil
}

// runBody executes a range body: the statement kinds run does, minus
// return, which cannot stand inside a body.
func (p *vmProgram) runBody(ctx context.Context, stmts []vmStmt, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) error {
	for i := range stmts {
		s := &stmts[i]
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
		if s.call == nil || s.ret {
			return fmt.Errorf("exec: a statement of this kind cannot stand inside a range body")
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
