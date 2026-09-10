package gozero

import (
	"fmt"
	"reflect"
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
// compileExpr compiles a call and the methods chained onto it,
// returning the outermost call and the static type of its first
// non-error result.
func (c *Compiler) compileExpr(sc *cscope, e *callExpr) (*vmCall, reflect.Type, error) {
	var (
		curr     *vmCall
		currType reflect.Type
		methods  []string
	)

	// The longest prefix of the path that names a binding wins, so
	// http.NewRequest is one name while req.Cookies is a method on req.
	base := -1
	for i := len(e.path); i >= 1; i-- {
		if _, ok := sc.bindings[joinPath(e.path[:i])]; ok {
			base = i
			break
		}
	}

	var recv *vmArg
	switch {
	case base >= 0:
		methods = e.path[base:]
	default:
		slot, ok := sc.slots[e.path[0]]
		if !ok {
			// A known import with an unknown symbol errors the way Go
			// spells it; anything else keeps the binding message.
			if sc.pkgs != nil && sc.pkgs[e.path[0]] != nil && len(e.path) > 1 {
				return nil, nil, fmt.Errorf("compile: undefined: %s", joinPath(e.path[:2]))
			}
			return nil, nil, fmt.Errorf("compile: unknown binding %q", joinPath(e.path))
		}
		recv = &vmArg{kind: vaSlot, slot: slot, typ: sc.env[e.path[0]], iface: -1}
		currType = sc.env[e.path[0]]
		methods = e.path[1:]
		if len(methods) == 0 && len(e.chain) == 0 {
			return nil, nil, fmt.Errorf("compile: %s is a value, not a call", e.path[0])
		}
	}

	// Arguments written in the source belong to the last name in the
	// path. Everything before it is called with no arguments, which is
	// legal because every argument is optional.
	links := make([]link, 0, len(methods)+len(e.chain))
	for i, m := range methods {
		l := link{name: m}
		if i == len(methods)-1 {
			l.args = e.args
		}
		links = append(links, l)
	}
	links = append(links, e.chain...)

	if base >= 0 {
		name := joinPath(e.path[:base])
		var args []arg
		if len(methods) == 0 {
			args = e.args
		}
		call, err := c.compileCall(sc, sc.bindings[name].rv, name, nil, args)
		if err != nil {
			return nil, nil, err
		}
		curr, currType = call, c.resultType(call, 0)
	}

	for _, l := range links {
		if curr != nil {
			recv = &vmArg{kind: vaCall, sub: curr, typ: currType}
		}
		if currType == nil {
			return nil, nil, fmt.Errorf("compile: cannot call %s on a value of unknown type", l.name)
		}
		if m, ok := currType.MethodByName(l.name); ok {
			fn := m.Func
			if !fn.IsValid() {
				// An interface type's Method carries no Func: there is
				// no concrete code to point at until a dynamic value is
				// behind the receiver. The wrapper dispatches on it, so
				// ctx.Err() compiles like any method call.
				fn = ifaceMethodFunc(currType, m)
			}
			call, err := c.compileCall(sc, fn, currType.String()+"."+l.name, recv, l.args)
			if err != nil {
				return nil, nil, err
			}
			curr, currType = call, c.resultType(call, 0)
			continue
		}
		// The pointer type's method set completes Go's rule: a pointer
		// receiver method is callable on a value as long as the value
		// is addressable, and a named value or a field of one is. This
		// also resolves promoted pointer-receiver methods, which only
		// *T's method set carries for an embedded value.
		if currType.Kind() != reflect.Pointer && currType.Kind() != reflect.Interface {
			if m, ok := reflect.PointerTo(currType).MethodByName(l.name); ok {
				arecv, err := addrOf(currType, recv)
				if err != nil {
					return nil, nil, fmt.Errorf("compile: cannot call pointer method %s on %s: %w", l.name, currType, err)
				}
				call, err := c.compileCall(sc, m.Func, currType.String()+"."+l.name, arecv, l.args)
				if err != nil {
					return nil, nil, err
				}
				curr, currType = call, c.resultType(call, 0)
				continue
			}
		}
		f, deref, ok := fieldOf(currType, l.name)
		if !ok {
			return nil, nil, fmt.Errorf("compile: %s has no method or field %s", currType, l.name)
		}
		if len(l.args) > 0 {
			return nil, nil, fmt.Errorf("compile: %s.%s is a field, not a method", currType, l.name)
		}
		// The field becomes the receiver of whatever comes next.
		// curr goes back to nil so the next link does not overwrite it
		// with a call result.
		recv = &vmArg{kind: vaField, src: recv, index: f.Index, deref: deref, typ: f.Type, iface: -1}
		curr, currType = nil, f.Type
	}

	if curr == nil {
		return nil, nil, fmt.Errorf("compile: %q is not callable", joinPath(e.path))
	}
	return curr, currType, nil
}

// addrOf turns a receiver into the address of the value it names. Only
// a name or a field chain rooted in one qualifies: those are variables
// and have an address, where a call's result does not, which is Go's
// addressability rule for pointer-receiver calls.
func addrOf(t reflect.Type, a *vmArg) (*vmArg, error) {
	root := a
	for root.kind == vaField {
		root = root.src
	}
	if root.kind != vaSlot {
		return nil, fmt.Errorf("the value is not addressable, bind it to a name first")
	}
	// Built fresh rather than copied: vmArg carries an atomic cache
	// that must not be copied.
	return &vmArg{
		kind: a.kind, slot: a.slot, name: a.name,
		src: a.src, index: a.index, deref: a.deref,
		addrOf: true, typ: reflect.PointerTo(t), iface: -1,
	}, nil
}

// ifaceMethodFunc builds a callable func value for a method of an
// interface type. MethodByName on an interface returns the signature
// without a receiver and a zero Func, so the call site gets a
// synthesized func whose first parameter is the interface and whose
// body dispatches on the dynamic value, exactly what the compiled
// method call on a concrete receiver gets from Method.Func. A nil
// receiver panics inside reflect the way a nil interface method call
// panics in Go, and arrives as *PanicError through the guard.
func ifaceMethodFunc(t reflect.Type, m reflect.Method) reflect.Value {
	mt := m.Type
	in := make([]reflect.Type, 0, mt.NumIn()+1)
	in = append(in, t)
	for i := 0; i < mt.NumIn(); i++ {
		in = append(in, mt.In(i))
	}
	out := make([]reflect.Type, 0, mt.NumOut())
	for i := 0; i < mt.NumOut(); i++ {
		out = append(out, mt.Out(i))
	}
	idx := m.Index
	variadic := mt.IsVariadic()
	return reflect.MakeFunc(reflect.FuncOf(in, out, variadic), func(args []reflect.Value) []reflect.Value {
		if variadic {
			// MakeFunc hands the variadic tail packed as a slice.
			return args[0].Method(idx).CallSlice(args[1:])
		}
		return args[0].Method(idx).Call(args[1:])
	})
}

// compileCall validates one call against a func value. recv is the
// receiver for a method call and fills parameter 0; src holds the
// arguments written in the source, which fill the parameters after it.
func (c *Compiler) compileCall(sc *cscope, fn reflect.Value, name string, recv *vmArg, src []arg) (*vmCall, error) {
	ft := fn.Type()

	call := &vmCall{fn: fn, name: name, errIdx: -1}
	if recv != nil {
		if !recv.typ.AssignableTo(ft.In(0)) {
			return nil, fmt.Errorf("compile: %s: receiver is %s, want %s", name, recv.typ, ft.In(0))
		}
		call.args = append(call.args, recv)
	}

	// Parameters and written arguments advance independently: a
	// context.Context parameter is filled from the execution context
	// and consumes no argument, unless the argument standing at that
	// position is itself statically a context, which is how a program
	// passes one it made on purpose.
	fixed := ft.NumIn()
	if ft.IsVariadic() {
		fixed--
	}
	j := 0
	for i := len(call.args); i < fixed; i++ {
		pt := ft.In(i)
		if pt == ctxType && !(j < len(src) && c.staticType(sc, src[j]) == ctxType) {
			call.args = append(call.args, &vmArg{kind: vaCtx, typ: pt, iface: -1})
			continue
		}
		// A parameter the source does not supply is its zero value:
		// "" for string, nil for io.Reader.
		if j >= len(src) {
			call.args = append(call.args, &vmArg{kind: vaConst, val: reflect.Zero(pt), typ: pt, iface: -1})
			continue
		}
		if src[j].spread {
			return nil, fmt.Errorf("compile: %s argument %d: ... spreads only into a variadic parameter", name, j+1)
		}
		a, err := c.compileArg(sc, name, j, pt, src[j])
		if err != nil {
			return nil, err
		}
		call.args = append(call.args, a)
		j++
	}

	rest := src[min(j, len(src)):]
	switch {
	case !ft.IsVariadic():
		if len(rest) > 0 {
			return nil, fmt.Errorf("compile: %s takes %d arguments, got %d", name, fixed-boolToInt(recv != nil), len(src))
		}
	case len(rest) == 1 && rest[0].spread:
		// f(xs...): the slice is passed whole and the invocation goes
		// through CallSlice.
		st := ft.In(fixed)
		a, err := c.compileArg(sc, name, j, st, unspread(rest[0]))
		if err != nil {
			return nil, err
		}
		call.args = append(call.args, a)
		call.spread = true
	default:
		// Individual arguments pack into the variadic slot, each
		// compiled against the element type; reflect.Value.Call packs
		// them the way a Go call site would.
		et := ft.In(fixed).Elem()
		for _, a := range rest {
			if a.spread {
				return nil, fmt.Errorf("compile: %s: ... must be the only variadic argument", name)
			}
			va, err := c.compileArg(sc, name, j, et, a)
			if err != nil {
				return nil, err
			}
			call.args = append(call.args, va)
			j++
		}
	}

	for i := 0; i < ft.NumOut(); i++ {
		if ft.Out(i) == errType {
			call.errIdx = i
		}
	}
	call.nres = ft.NumOut()
	if call.errIdx >= 0 {
		call.nres--
	}
	return call, nil
}

func (c *Compiler) compileArg(sc *cscope, name string, pos int, pt reflect.Type, a arg) (*vmArg, error) {
	var v reflect.Value
	switch a.kind {
	case argBool:
		v = reflect.ValueOf(a.b)
	case argNil:
		z, err := nilAs(pt)
		if err != nil {
			return nil, fmt.Errorf("compile: %s argument %d: %w", name, pos+1, err)
		}
		return &vmArg{kind: vaConst, val: z, typ: pt, iface: -1}, nil
	case argString:
		v = reflect.ValueOf(a.str)
	case argInt, argFloat:
		lit, err := literalAs(pt, a)
		if err != nil {
			return nil, fmt.Errorf("compile: %s argument %d: %w", name, pos+1, err)
		}
		v = lit
	case argCall:
		sub, st, err := c.compileExpr(sc, a.sub)
		if err != nil {
			return nil, err
		}
		if sub.nres == 0 {
			return nil, fmt.Errorf("compile: %s argument %d: %s returns no value", name, pos+1, sub.name)
		}
		if !st.AssignableTo(pt) {
			return nil, fmt.Errorf("compile: %s argument %d: cannot use %s as %s", name, pos+1, st, pt)
		}
		return &vmArg{kind: vaCall, sub: sub, typ: pt, iface: -1}, nil
	case argPath:
		slot, ok := sc.slots[a.path[0]]
		if !ok {
			return nil, fmt.Errorf("compile: %s argument %d: %s is not a name bound by the program, so its fields are unknown", name, pos+1, a.path[0])
		}
		cur := &vmArg{kind: vaSlot, slot: slot, name: a.path[0], typ: sc.env[a.path[0]], iface: -1}
		curType := sc.env[a.path[0]]
		for _, seg := range a.path[1:] {
			f, deref, ok := fieldOf(curType, seg)
			if !ok {
				return nil, fmt.Errorf("compile: %s argument %d: %s has no field %s", name, pos+1, curType, seg)
			}
			cur = &vmArg{kind: vaField, src: cur, index: f.Index, deref: deref, typ: f.Type, iface: -1}
			curType = f.Type
		}
		if !curType.AssignableTo(pt) {
			return nil, fmt.Errorf("compile: %s argument %d: cannot use %s as %s", name, pos+1, curType, pt)
		}
		cur.typ = pt
		return cur, nil

	case argStruct:
		sa, st, err := c.compileStructLit(sc, a)
		if err != nil {
			return nil, fmt.Errorf("compile: %s argument %d: %w", name, pos+1, err)
		}
		if !st.AssignableTo(pt) {
			return nil, fmt.Errorf("compile: %s argument %d: cannot use %s as %s", name, pos+1, sc.typeName(st), sc.typeName(pt))
		}
		sa.typ = pt
		return sa, nil

	case argVar:
		if a.str == "dest" {
			return &vmArg{kind: vaDest, name: "dest", typ: pt, iface: -1}, nil
		}
		if slot, ok := sc.slots[a.str]; ok {
			st := sc.env[a.str]
			if st != nil && !st.AssignableTo(pt) {
				return nil, fmt.Errorf("compile: %s argument %d: cannot use %s as %s", name, pos+1, st, pt)
			}
			return &vmArg{kind: vaSlot, slot: slot, name: a.str, typ: pt, iface: -1}, nil
		}
		// Not bound by the program, so it comes off the caller's stack
		// and its type is only known when it is read.
		return &vmArg{kind: vaStack, name: a.str, typ: pt, iface: -1}, nil
	}
	if !v.IsValid() {
		return nil, fmt.Errorf("compile: %s argument %d: unsupported literal", name, pos+1)
	}
	if !v.Type().AssignableTo(pt) {
		return nil, fmt.Errorf("compile: %s argument %d: cannot use %s as %s", name, pos+1, v.Type(), pt)
	}
	return &vmArg{kind: vaConst, val: v, typ: pt, iface: -1}, nil
}
