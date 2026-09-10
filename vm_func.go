package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// Script functions. A declaration or literal compiles to its own
// vmProgram unit with its own slot space; a literal additionally
// captures the enclosing variables it reads. Capture is by cell: the
// enclosing unit marks a captured slot address-taken, so its storage
// is an addressable cell writes go through, and the closure receives
// the cell itself, which is Go's capture-by-variable.

// scriptFn is one compiled function.
type scriptFn struct {
	name    string
	sig     reflect.Type // the declared signature as a func type
	params  []int        // parameter slots, receiver first for methods
	results []reflect.Type
	errIdx  int // trailing error result, -1 when there is none
	unit    *vmProgram
	// capLocal are this unit's slots that receive the captured cells;
	// capsOuter are the enclosing unit's slots they come from, in the
	// same order.
	capLocal  []int
	capsOuter []int
	// paramNames are the declared parameter names in order, receiver
	// included, for calling by stack map.
	paramNames []string
	recvT    reflect.Type // method receiver type, nil for a plain func
}

// call runs the function: fresh unit memory per call, captured cells
// installed first, parameters stored through the cell-aware setter.
// The stack map and dest do not cross a function boundary; a body
// sees its parameters and captures, as a Go function does.
func (fn *scriptFn) call(ctx context.Context, env []reflect.Value, args []reflect.Value) ([]reflect.Value, error) {
	u := fn.unit
	mem := make([]reflect.Value, u.nslots+u.frame)
	slots, frame := mem[:u.nslots], mem[u.nslots:]
	var ifaces []ifacePair
	if u.nifaces > 0 {
		ifaces = make([]ifacePair, u.nifaces)
	}
	for i := range env {
		slots[fn.capLocal[i]] = env[i]
	}
	for _, in := range u.inits {
		slots[in.slot] = reflect.New(in.zero.Type()).Elem()
	}
	for i, a := range args {
		if i < len(fn.params) {
			u.setSlot(slots, a, fn.params[i])
		}
	}
	var defers deferStack
	var ret any
	var err error
	var sig ctlSig
	func() {
		defer func() {
			derr := defers.runAll()
			if err == nil {
				err = derr
			}
		}()
		sig, ret, err = u.runBlock(ctx, slots, frame, ifaces, nil, nil, &defers, u.stmts)
	}()
	if err != nil {
		return nil, err
	}
	if len(fn.results) == 0 {
		return nil, nil
	}
	if sig != sigReturn {
		return nil, fmt.Errorf("exec: %s: missing return", fn.name)
	}
	out, ok := ret.([]reflect.Value)
	if !ok || len(out) != len(fn.results) {
		return nil, fmt.Errorf("exec: %s: return arity mismatch", fn.name)
	}
	return out, nil
}

// materialize builds the Go func value for a literal or a bridged
// declaration: an ordinary reflect.MakeFunc of the requested func
// type, closing over the captured cells and the creation context.
// The requested type may be a named func type, so a conversion like
// handler-func adapters gets a value that carries the named type's
// method set.
func (fn *scriptFn) materialize(ctx context.Context, ft reflect.Type, env []reflect.Value) reflect.Value {
	return reflect.MakeFunc(ft, func(args []reflect.Value) []reflect.Value {
		out, err := fn.call(ctx, env, args)
		if err != nil {
			// With a trailing error result the error travels out as a
			// value; anything else panics the way a Go runtime failure
			// does, and the host's guard turns it into *PanicError.
			if fn.errIdx >= 0 {
				out = zeroResults(fn.results)
				out[fn.errIdx] = reflect.ValueOf(&err).Elem()
				return out
			}
			panic(err)
		}
		if out == nil {
			return zeroResults(fn.results)
		}
		return out
	})
}

// materializeMethod is materialize with the receiver bound: the
// returned func value has the receiverless signature and prepends
// recv on every call.
func (fn *scriptFn) materializeMethod(ctx context.Context, ft reflect.Type, recv reflect.Value) reflect.Value {
	return reflect.MakeFunc(ft, func(args []reflect.Value) []reflect.Value {
		full := make([]reflect.Value, 0, len(args)+1)
		full = append(full, recv)
		full = append(full, args...)
		out, err := fn.call(ctx, nil, full)
		if err != nil {
			if fn.errIdx >= 0 {
				out = zeroResults(fn.results)
				out[fn.errIdx] = reflect.ValueOf(&err).Elem()
				return out
			}
			panic(err)
		}
		if out == nil {
			return zeroResults(fn.results)
		}
		return out
	})
}

func zeroResults(results []reflect.Type) []reflect.Value {
	out := make([]reflect.Value, len(results))
	for i, t := range results {
		out[i] = reflect.Zero(t)
	}
	return out
}

// capturedCells reads the enclosing run's cells for a capture list.
func capturedCells(slots []reflect.Value, caps []int) []reflect.Value {
	if len(caps) == 0 {
		return nil
	}
	env := make([]reflect.Value, len(caps))
	for i, s := range caps {
		env[i] = slots[s]
	}
	return env
}
