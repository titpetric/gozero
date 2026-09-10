package gozero

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"unsafe"
)

// The multi-statement VM. A program is a list of calls whose results
// are bound to names; there are no operators, so every value is a
// literal, a name, or the result of another call.
//
// Two rules shape the compiled form:
//
// A trailing error result is never a value. It is stripped from the
// result list at compile time and checked after every call, so
// "req := http.NewRequest(...)" binds one name to a two-result call and
// a failure returns from Exec or Scan on the spot. No program text
// mentions an error.
//
// Every argument is optional. A parameter the statement does not supply
// is filled with the zero value of its type, which is how
// http.NewRequest is called with two arguments and a nil body.
//
// Names bound by the program carry a static type, so methods on them
// are resolved against that type when the statement compiles.
// json.NewEncoder is bound; Encode is not, and is reached through the
// *json.Encoder the binding returns. Names coming from the caller's
// stack, including dest, are opaque and are checked when they are read.

type vmArgKind int

const (
	vaConst vmArgKind = iota // a literal, or an omitted argument's zero value
	vaSlot                   // a name bound earlier in the program
	vaStack                  // a name read from the caller's stack map
	vaDest                   // the pointer Scan was handed
	vaCall                   // a nested call
	vaField                  // a struct field read off another value
	vaCtx                    // the execution context, auto-filled
	vaStruct                 // a composite literal, built fresh per evaluation
	vaBinary                 // op over x and y; && and || short-circuit
	vaUnary                  // op over x
	vaIndex                  // x[y] on a slice, array, string or map
	vaLen                    // len(x)
)

var ctxType = reflect.TypeFor[context.Context]()

// vmArg is one argument of a compiled call.
//
// assign is an inline cache for the assignability check on vaStack and
// vaDest, the only two kinds whose type is unknown until the value is
// read. reflect.Type.AssignableTo against an interface parameter walks
// the method tables comparing names, and it dominated the profile at
// 29% of samples; the same program is almost always handed the same
// concrete type, so one entry removes it. It is an atomic.Pointer
// rather than a plain field because a compiled program is shared
// between concurrent runs.
type vmArg struct {
	kind   vmArgKind
	val    reflect.Value // vaConst
	slot   int           // vaSlot
	name   string        // vaStack
	typ    reflect.Type  // the parameter type this argument fills
	sub    *vmCall       // vaCall
	assign atomic.Pointer[assignCache]

	// iface >= 0 when this argument fills a non-empty interface
	// parameter from a dynamic value and the interface type is in
	// ifaceConvs. The value is converted with a type assertion, which
	// is how the itab is obtained, and handed to reflect already typed
	// as the parameter. reflect.Value.call runs its own assignTo on
	// every argument, and for an interface parameter that means
	// reflect.implements walking method tables by name on every call;
	// arriving with identical types skips it.
	iface int
	conv  ifaceConv

	// vaField: src is the value the field is read from, index the
	// reflect field path, and deref says src is a pointer to the struct
	// rather than the struct.
	src   *vmArg
	index []int
	deref bool

	// addrOf resolves to the address of the slot or field instead of
	// its value: the receiver of a pointer method on a value. typ is
	// then the pointer type. Only a name or a field of one is
	// addressable, matching Go's rule that a variable has an address
	// and the result of a call does not.
	addrOf bool

	// vaStruct: styp is the struct type the literal builds, addr marks
	// the &T{} form, and elems are the field writes. The value is built
	// on every evaluation, the way a Go composite literal allocates
	// each time the expression runs; a value shared between runs would
	// share its mutations.
	styp  reflect.Type
	addr  bool
	elems []vmElem

	// vaBinary, vaUnary, vaIndex, vaLen: the operands and, chosen at
	// compile time, the evaluator closure carrying the operator's
	// native semantics. binFn is nil for the forms get evaluates
	// itself: the short-circuit ops and the nil comparisons.
	op    string
	x, y  *vmArg
	binFn func(x, y reflect.Value) reflect.Value
	unFn  func(x reflect.Value) reflect.Value
}

// vmElem is one element of a compiled composite literal: the field it
// fills and the value.
type vmElem struct {
	index []int
	val   *vmArg
}

// assignCache is one remembered answer to "is this concrete type
// assignable to the parameter type".
type assignCache struct {
	typ reflect.Type
	ok  bool
}

// assignable answers for rt, consulting and filling the inline cache.
func (a *vmArg) assignable(rt reflect.Type) bool {
	if c := a.assign.Load(); c != nil && c.typ == rt {
		return c.ok
	}
	ok := rt.AssignableTo(a.typ)
	a.assign.Store(&assignCache{typ: rt, ok: ok})
	return ok
}

// vmCall is one compiled call. A method call is the same structure with
// the receiver as args[0], because reflect.Method.Func takes the
// receiver as its first parameter.
type vmCall struct {
	fn     reflect.Value
	name   string // for diagnostics
	args   []*vmArg
	errIdx int // index of the trailing error result, -1 when there is none
	nres   int // results excluding that error
	off    int // this call's window into the per-run frame
	// spread marks a variadic call whose last argument is the slice
	// itself, f(xs...): the invocation goes through CallSlice.
	spread bool
	// bindErr marks a call whose trailing error the program named on
	// its left-hand side: the error is a value there, not the
	// implicit check.
	bindErr bool
}

// vmStmt is one statement: a call, the slots its results bind to, and
// whether it ends the program.
type vmStmt struct {
	call *vmCall
	out  []int // one slot per bound result
	ret  bool

	// lit is the value of a literal assignment, "x = 123", already
	// converted to the slot's type.
	lit reflect.Value

	// assign is an assignment whose value is built per run rather than
	// precomputed: a composite literal, "u = url.URL{...}". lit cannot
	// carry it, because runs sharing one prebuilt value would share
	// the struct.
	assign *vmArg

	// retArg is a return statement's value when it is not a call:
	// a name, a field read, or a literal.
	retArg *vmArg

	// fieldSet writes a value through a field: req.Method = "POST".
	fieldSet *vmFieldSet

	// recv is a channel receive, its value and ok slots in out; send
	// is a channel send. Both in vm_chan.go.
	recv *vmRecv
	send *vmSend

	// Control flow, in vm_flow.go; init re-zeroes a block-scoped var
	// on every pass over its declaration.
	ifs  *vmIf
	loop *vmFor
	rng  *vmRange
	brk  bool
	cont bool
	init *slotInit

	// deferCall runs when the program exits, its arguments already
	// evaluated at the defer statement.
	deferCall *vmCall
}

// fieldStep is one selector of a field-assignment target.
type fieldStep struct {
	index []int
	deref bool
}

// vmFieldSet is a compiled field assignment: the slot the base name
// lives in, the selectors to the field, and the value.
type vmFieldSet struct {
	base  int // slot of the base name
	steps []fieldStep
	val   *vmArg
	field string // for diagnostics
}

// slotInit is the zero value a var statement puts in scope before the
// program runs.
type slotInit struct {
	slot int
	zero reflect.Value
}

// vmProgram is a compiled program. It holds no per-call state: the
// slots are allocated per execution, so a compiled program is safe for
// concurrent use.
type vmProgram struct {
	stmts   []vmStmt
	nslots  int
	frame   int // total argument words across every call in the program
	nifaces int // interface arguments pre-converted per run

	// slotTypes is the static type of each named slot, which the step
	// JIT needs to lay out its frame. polymorphic records a slot
	// reassigned at a different type, which no single frame field can
	// hold.
	slotTypes   []reflect.Type
	polymorphic bool

	// addrTaken marks the slots whose address the program takes, so
	// run stores their values through an addressable cell. The step
	// JIT reads it too: an addressed slot cannot back an aliased
	// interface argument or have its producer spliced away.
	addrTaken map[int]bool

	// inits are the var declarations, applied before the first
	// statement so a name reads as its type's zero value even when
	// nothing assigned it.
	inits []slotInit
}

// apply writes the value through the field chain. Addressability comes
// from a pointer in the chain; a struct held by value in a slot is only
// settable when the slot was created addressable by a var declaration.
func (fs *vmFieldSet) apply(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) error {
	v := slots[fs.base]
	if !v.IsValid() {
		return fmt.Errorf("exec: %s: the base is not set", fs.field)
	}
	for _, st := range fs.steps {
		if st.deref {
			if v.IsNil() {
				return fmt.Errorf("exec: %s: field write on a nil %s", fs.field, v.Type())
			}
			v = v.Elem()
		}
		v = v.FieldByIndex(st.index)
	}
	if !v.CanSet() {
		return fmt.Errorf("exec: %s: the value is not addressable, declare the base with var or hold it behind a pointer", fs.field)
	}
	val, err := fs.val.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return err
	}
	v.Set(val)
	return nil
}

// run executes the program. Slots are allocated per execution, so
// concurrent runs of the same compiled program do not share state.
func (p *vmProgram) run(ctx context.Context, stack map[string]any, dest any) (ret any, err error) {
	// One allocation per run for both the named slots and every call's
	// argument window. Each call owns a disjoint range, so a nested
	// call never overwrites the arguments its parent is still filling.
	mem := make([]reflect.Value, p.nslots+p.frame)
	slots, frame := mem[:p.nslots], mem[p.nslots:]
	var ifaces []ifacePair
	if p.nifaces > 0 {
		ifaces = make([]ifacePair, p.nifaces)
	}
	for _, in := range p.inits {
		// New rather than Zero, so a field of a var-declared struct is
		// settable in place.
		slots[in.slot] = reflect.New(in.zero.Type()).Elem()
	}
	var defers deferStack
	defer func() {
		// A Go defer, so the deferred calls also run while a panic
		// from a binding unwinds, as they would in compiled Go.
		derr := defers.runAll()
		if err == nil {
			err = derr
		}
	}()
	_, ret, err = p.runBlock(ctx, slots, frame, ifaces, stack, dest, &defers, p.stmts)
	return ret, err
}

// addrCell stores v into a fresh addressable cell when the slot's
// address is taken somewhere in the program. A call result and the
// prebuilt literal value are not addressable, and the literal is also
// shared between runs, so both go through the copy; every other slot
// keeps the value as it is.
func (p *vmProgram) addrCell(v reflect.Value, slot int) reflect.Value {
	if !p.addrTaken[slot] {
		return v
	}
	cell := reflect.New(v.Type()).Elem()
	cell.Set(v)
	return cell
}

func firstNonErr(out []reflect.Value, errIdx int) reflect.Value {
	for i := range out {
		if i != errIdx {
			return out[i]
		}
	}
	return reflect.Value{}
}

// invoke evaluates the arguments and calls the func, returning the raw
// result list. A non-nil trailing error stops the program.
func (c *vmCall) invoke(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) ([]reflect.Value, error) {
	args := frame[c.off : c.off+len(c.args)]
	for i, a := range c.args {
		v, err := a.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	var out []reflect.Value
	if c.spread {
		out = c.fn.CallSlice(args)
	} else {
		out = c.fn.Call(args)
	}
	if c.errIdx >= 0 && !c.bindErr {
		if e := out[c.errIdx]; !e.IsNil() {
			return nil, e.Interface().(error)
		}
	}
	return out, nil
}

// get resolves one argument for this call.
func (a *vmArg) get(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) (reflect.Value, error) {
	switch a.kind {
	case vaConst:
		return a.val, nil
	case vaSlot:
		v := slots[a.slot]
		if a.addrOf {
			if !v.IsValid() || !v.CanAddr() {
				return reflect.Value{}, fmt.Errorf("exec: cannot take the address of %s", a.name)
			}
			return v.Addr(), nil
		}
		if !v.IsValid() {
			return reflect.Zero(a.typ), nil
		}
		return v, nil
	case vaCtx:
		return reflect.ValueOf(ctx), nil
	case vaCall:
		out, err := a.sub.invoke(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return reflect.Value{}, err
		}
		return firstNonErr(out, a.sub.errIdx), nil
	case vaField:
		v, err := a.src.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return reflect.Value{}, err
		}
		if a.deref {
			if v.IsNil() {
				return reflect.Value{}, fmt.Errorf("exec: field read on a nil %s", v.Type())
			}
			v = v.Elem()
		}
		v = v.FieldByIndex(a.index)
		if a.addrOf {
			if !v.CanAddr() {
				return reflect.Value{}, fmt.Errorf("exec: cannot take the address of the field")
			}
			return v.Addr(), nil
		}
		return v, nil
	case vaStruct:
		// New rather than a copied prototype, so every evaluation is its
		// own allocation and the result is addressable: a field of the
		// value a slot holds can be assigned afterwards.
		pv := reflect.New(a.styp)
		sv := pv.Elem()
		for i := range a.elems {
			v, err := a.elems[i].val.get(ctx, slots, frame, ifaces, stack, dest)
			if err != nil {
				return reflect.Value{}, err
			}
			sv.FieldByIndex(a.elems[i].index).Set(v)
		}
		if a.addr {
			return pv, nil
		}
		return sv, nil
	case vaBinary:
		return a.evalBinary(ctx, slots, frame, ifaces, stack, dest)
	case vaUnary:
		v, err := a.x.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return reflect.Value{}, err
		}
		return a.unFn(v), nil
	case vaIndex:
		return a.evalIndex(ctx, slots, frame, ifaces, stack, dest)
	case vaLen:
		v, err := a.x.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(v.Len()), nil
	case vaDest:
		if dest == nil {
			return reflect.Zero(a.typ), fmt.Errorf("exec: dest is only set by Scan")
		}
		return a.dynamic(dest, ifaces, "dest")
	case vaStack:
		v, ok := stack[a.name]
		if !ok || v == nil {
			return reflect.Zero(a.typ), nil
		}
		return a.dynamic(v, ifaces, a.name)
	}
	return reflect.Value{}, fmt.Errorf("exec: argument kind %d cannot be resolved", a.kind)
}

// dynamic resolves a value whose type is only known now: from the
// caller's stack, or from the pointer Scan was handed.
func (a *vmArg) dynamic(v any, ifaces []ifacePair, name string) (reflect.Value, error) {
	if a.iface >= 0 {
		// The assertion inside conv is both the check and the itab
		// lookup. Writing the result into the frame gives reflect a
		// value already typed as the parameter.
		p, ok := a.conv(v)
		if !ok {
			return reflect.Value{}, fmt.Errorf("exec: variable %q: cannot use %T as %s", name, v, a.typ)
		}
		ifaces[a.iface] = p
		return reflect.NewAt(a.typ, unsafe.Pointer(&ifaces[a.iface])).Elem(), nil
	}
	rv := reflect.ValueOf(v)
	if !a.assignable(rv.Type()) {
		return reflect.Value{}, fmt.Errorf("exec: variable %q: cannot use %s as %s", name, rv.Type(), a.typ)
	}
	return rv, nil
}
