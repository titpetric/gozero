package gozero

import (
	"fmt"
	"reflect"
)

// The step statements, "n++;" and "n--;". The step is the untyped 1
// of the Go spec, applied at the slot's own type, and the result
// wraps at the type's width exactly as compiled Go wraps. Only a
// name bound by the program steps: a stack name is read-only
// everywhere in the language, and a field target has no slot.

// vmInc is a compiled step statement: the slot the name lives in and
// the step, +1 for ++ and -1 for --.
type vmInc struct {
	slot  int
	delta int64
	name  string // "n++", for diagnostics
}

// compileInc compiles n++ and n--. The name must already be bound by
// the program, and its type must be an integer or float: ++ and --
// step numeric types, and every other type is rejected here, at
// compile time.
func (c *Compiler) compileInc(slots map[string]int, env map[string]reflect.Type, s stmt) (*vmInc, error) {
	op := incDecOp(s.incDelta)
	slot, ok := slots[s.incName]
	if !ok {
		return nil, fmt.Errorf("compile: %s%s: %s is not defined, use := or var", s.incName, op, s.incName)
	}
	t := env[s.incName]
	if t == nil || !stepsAt(t.Kind()) {
		return nil, fmt.Errorf("compile: %s%s: %s is %s, ++ and -- step an integer or float", s.incName, op, s.incName, t)
	}
	return &vmInc{slot: slot, delta: s.incDelta, name: s.incName + op}, nil
}

// stepsAt reports whether ++ and -- are defined at a kind: the
// integer and float kinds. Go also steps complex numbers; no layout
// class carries one, so they stay rejected by the same rule.
func stepsAt(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// exec applies the step. inPlace says the slot's address is taken
// somewhere in the program: the cell is then per-run storage (see
// addrCell), and stepping it where it is keeps a pointer taken
// earlier seeing the new value. Any other slot gets a fresh cell,
// because the value it holds may be the compiled program's own
// prebuilt literal, shared between concurrent runs.
func (in *vmInc) exec(slots []reflect.Value, inPlace bool) error {
	v := slots[in.slot]
	if !v.IsValid() {
		return fmt.Errorf("exec: %s: the name is not set", in.name)
	}
	if !inPlace || !v.CanSet() {
		cell := reflect.New(v.Type()).Elem()
		cell.Set(v)
		v = cell
		slots[in.slot] = v
	}
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		v.SetFloat(v.Float() + float64(in.delta))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		// SetUint checks the width, so the sum is masked to it first,
		// which is the wraparound compiled Go performs.
		u := v.Uint() + uint64(in.delta)
		if bits := v.Type().Bits(); bits < 64 {
			u &= 1<<bits - 1
		}
		v.SetUint(u)
	default:
		// The signed widths truncate by shifting the sum's low bits
		// back down, which sign-extends them the way a Go conversion
		// to the narrower type does.
		n := v.Int() + in.delta
		if bits := v.Type().Bits(); bits < 64 {
			sh := 64 - bits
			n = n << sh >> sh
		}
		v.SetInt(n)
	}
	return nil
}
