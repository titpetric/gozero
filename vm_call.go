package gozero

import (
	"fmt"
	"reflect"
)

// The multi-statement VM. A program is a list of calls that bind their
// results to names. Because there are no operators, every value is a
// literal, a name, or the result of another call.
//
// Two rules shape the compiled form:
//
// A trailing error result is never a value. Because the compiler strips
// it from the result list and checks it after every call,
// "req := http.NewRequest(...)" binds one name to a two-result call and
// a failure returns from Exec or Scan at that statement. No program text
// mentions an error.
//
// Every argument is optional. A parameter the statement does not supply
// takes the zero value of its type; http.NewRequest with two arguments
// receives a nil body that way.
//
// Because names the program binds have a static type, the compiler
// resolves methods on them against that type when the statement
// compiles.
// json.NewEncoder is a binding; Encode is not. The program reaches it
// through the *json.Encoder the binding returns. Names coming from the
// caller's stack, including dest, are opaque and the VM checks them
// when it reads them.
// compileExpr compiles a call and the methods chained onto it,
// returning the outermost call and its first non-error result's
// static type.
func (c *Compiler) compileExpr(slots map[string]int, env map[string]reflect.Type, e *callExpr) (*vmCall, reflect.Type, error) {
	var (
		curr     *vmCall
		currType reflect.Type
		methods  []string
	)

	// Because the longest prefix of the path that names a binding wins,
	// http.NewRequest is one name while req.Cookies is a method on req.
	base := -1
	for i := len(e.path); i >= 1; i-- {
		if _, ok := c.bindings[joinPath(e.path[:i])]; ok {
			base = i
			break
		}
	}

	var recv *vmArg
	switch {
	case base >= 0:
		methods = e.path[base:]
	default:
		slot, ok := slots[e.path[0]]
		if !ok {
			// Not a program name either: a value binding can be the
			// receiver. It owns the longest dotted prefix of the path
			// the way a func binding does. Everything after that prefix
			// is fields and methods, which the link loop below already
			// tells apart.
			vb, rest, found := c.varOf(e.path)
			if !found {
				return nil, nil, fmt.Errorf("compile: unknown binding %q", joinPath(e.path))
			}
			base := len(e.path) - len(rest)
			recv = &vmArg{
				kind: vaVar, name: joinPath(e.path[:base]), varv: vb.val,
				typ: vb.val.Type(), iface: -1,
			}
			currType = vb.val.Type()
			methods = rest
			if len(rest) == 0 && len(e.chain) == 0 {
				// The name itself is the callee: a func bound as a
				// value, which the value registry answers and the
				// func registry does not hold.
				call, err := c.callThrough(slots, env, recv, currType, joinPath(e.path), e.args)
				if err != nil {
					return nil, nil, err
				}
				return call, c.resultType(call, 0), nil
			}
			break
		}
		recv = &vmArg{kind: vaSlot, slot: slot, name: e.path[0], typ: env[e.path[0]], iface: -1}
		currType = env[e.path[0]]
		methods = e.path[1:]
		if len(methods) == 0 && len(e.chain) == 0 {
			// "f()" where f is a name holding a func.
			call, err := c.callThrough(slots, env, recv, currType, e.path[0], e.args)
			if err != nil {
				return nil, nil, err
			}
			return call, c.resultType(call, 0), nil
		}
	}

	// Arguments written in the source belong to the last name in the
	// path. Everything before it runs with no arguments, which Go
	// semantics here permit because every argument is optional.
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
		call, err := c.compileCall(slots, env, c.bindings[name].rv, name, nil, args)
		if err != nil {
			return nil, nil, err
		}
		curr, currType = call, callResultType(call, 0)
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
				// An interface type's Method has no Func: there is
				// no concrete code to point at until a dynamic value is
				// behind the receiver. Because the wrapper dispatches on
				// it, ctx.Err() compiles like any method call.
				fn = ifaceMethodFunc(currType, m)
			}
			call, err := c.compileCall(slots, env, fn, currType.String()+"."+l.name, recv, l.args)
			if err != nil {
				return nil, nil, err
			}
			curr, currType = call, callResultType(call, 0)
			continue
		}
		// The pointer type's method set completes Go's rule: a pointer
		// receiver method is callable on a value as long as the value
		// is addressable, and a named value or a field of one is. This
		// also resolves promoted pointer-receiver methods, which only
		// *T's method set lists for an embedded value.
		if currType.Kind() != reflect.Pointer && currType.Kind() != reflect.Interface {
			if m, ok := reflect.PointerTo(currType).MethodByName(l.name); ok {
				arecv, err := addrOf(currType, recv)
				if err != nil {
					return nil, nil, fmt.Errorf("compile: cannot call pointer method %s on %s: %w", l.name, currType, err)
				}
				call, err := c.compileCall(slots, env, m.Func, currType.String()+"."+l.name, arecv, l.args)
				if err != nil {
					return nil, nil, err
				}
				curr, currType = call, callResultType(call, 0)
				continue
			}
		}
		f, deref, ok := fieldOf(currType, l.name)
		if !ok {
			return nil, nil, fmt.Errorf("compile: %s has no method or field %s", currType, l.name)
		}
		if f.Type.Kind() == reflect.Func && last(links, l) {
			// A func-typed field is a callee, not a receiver: each
			// run reads the value out of the struct and calls it.
			fv := &vmArg{kind: vaField, src: recv, index: f.Index, deref: deref, typ: f.Type, iface: -1}
			call, err := c.callThrough(slots, env, fv, f.Type, currType.String()+"."+l.name, l.args)
			if err != nil {
				return nil, nil, err
			}
			curr, currType = call, c.resultType(call, 0)
			continue
		}
		if last(links, l) {
			// Because the source wrote parentheses, the error names the
			// non-func field as the mistake: the chain ends here.
			return nil, nil, fmt.Errorf("compile: %s.%s is a %s field, not a method", currType, l.name, f.Type)
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

// last reports whether l is the final link. Because the source wrote
// arguments for that link alone, a func-typed value is the callee
// there and a receiver at any earlier link.
func last(links []link, l link) bool {
	return len(links) > 0 && links[len(links)-1].name == l.name
}

// callThrough compiles a call whose callee is a value: a func-typed
// name, value binding, or field. Because the signature is static, the
// compiler checks the arguments exactly as it checks a call to a
// binding; each run reads only the func value itself.
func (c *Compiler) callThrough(slots map[string]int, env map[string]reflect.Type, fv *vmArg, ft reflect.Type, name string, src []arg) (*vmCall, error) {
	if ft == nil || ft.Kind() != reflect.Func {
		return nil, fmt.Errorf("compile: %s is a value, not a call", name)
	}
	if ft.IsVariadic() {
		return nil, fmt.Errorf("compile: %s: a variadic func value is not callable yet", name)
	}
	return c.compileCallOf(slots, env, reflect.Value{}, fv, ft, name, nil, src)
}

// addrOf turns a receiver into the address of the value it names. A
// name, a value binding, or a field chain rooted in either qualifies:
// those are variables and have an address, where a call's result does
// not, which is Go's addressability rule for pointer-receiver calls.
//
// Because a value binding's address is the address of the program's
// per-run cell, a pointer method mutates the copy and not the host,
// the same storage "&name" and "name = v" reach.
func addrOf(t reflect.Type, a *vmArg) (*vmArg, error) {
	root := a
	for root.kind == vaField {
		root = root.src
	}
	if root.kind != vaSlot && root.kind != vaVar {
		return nil, fmt.Errorf("the value is not addressable, bind it to a name first")
	}
	// Built fresh, never copied: vmArg holds an atomic cache that a
	// copy would break.
	return &vmArg{
		kind: a.kind, slot: a.slot, name: a.name, varv: a.varv,
		src: a.src, index: a.index, deref: a.deref,
		addrOf: true, typ: reflect.PointerTo(t), iface: -1,
	}, nil
}

// ifaceMethodFunc builds a callable func value for a method of an
// interface type. Because MethodByName on an interface returns the
// signature without a receiver and a zero Func, the call site gets a
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
			// MakeFunc passes the variadic tail packed as a slice.
			return args[0].Method(idx).CallSlice(args[1:])
		}
		return args[0].Method(idx).Call(args[1:])
	})
}

// compileCall validates one call against a func value. recv is the
// receiver for a method call and fills parameter 0; src holds the
// arguments written in the source, which fill the parameters after it.
func (c *Compiler) compileCall(slots map[string]int, env map[string]reflect.Type, fn reflect.Value, name string, recv *vmArg, src []arg) (*vmCall, error) {
	return c.compileCallOf(slots, env, fn, nil, fn.Type(), name, recv, src)
}

// compileCallOf is compileCall with the callee split from its
// signature. A call through a func value then checks its arguments the
// same way a call to a binding does. Exactly one of fn and fnArg holds
// the callee; ft is the signature either way.
func (c *Compiler) compileCallOf(slots map[string]int, env map[string]reflect.Type, fn reflect.Value, fnArg *vmArg, ft reflect.Type, name string, recv *vmArg, src []arg) (*vmCall, error) {
	call := &vmCall{fn: fn, fnArg: fnArg, ft: ft, name: name, errIdx: -1}
	if recv != nil {
		if !recv.typ.AssignableTo(ft.In(0)) {
			return nil, fmt.Errorf("compile: %s: receiver is %s, want %s", name, recv.typ, ft.In(0))
		}
		call.args = append(call.args, recv)
	}

	// Parameters and written arguments advance independently: a
	// context.Context parameter takes the execution context and
	// consumes no argument, unless the argument standing at that
	// position is itself statically a context: a program passes a
	// context it made that way.
	fixed := ft.NumIn()
	if ft.IsVariadic() {
		fixed--
	}
	j := 0
	for i := len(call.args); i < fixed; i++ {
		pt := ft.In(i)
		if pt == ctxType && !(j < len(src) && c.staticType(env, src[j]) == ctxType) {
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
		a, err := c.compileArg(slots, env, name, j, pt, src[j])
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
			params := fixed
			if recv != nil {
				params--
			}
			return nil, fmt.Errorf("compile: %s takes %d arguments, got %d", name, params, len(src))
		}
	case len(rest) == 1 && rest[0].spread:
		// f(xs...): the call passes the slice whole and the invocation
		// uses CallSlice.
		st := ft.In(fixed)
		a, err := c.compileArg(slots, env, name, j, st, unspread(rest[0]))
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
			va, err := c.compileArg(slots, env, name, j, et, a)
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

func (c *Compiler) compileArg(slots map[string]int, env map[string]reflect.Type, name string, pos int, pt reflect.Type, a arg) (*vmArg, error) {
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
		sub, st, err := c.compileExpr(slots, env, a.sub)
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
	case argAddr:
		va, vt, err := c.addrArg(slots, env, a.path)
		if err != nil {
			return nil, fmt.Errorf("compile: %s argument %d: %w", name, pos+1, err)
		}
		if !vt.AssignableTo(pt) {
			return nil, fmt.Errorf("compile: %s argument %d: cannot use %s as %s", name, pos+1, vt, pt)
		}
		va.typ = pt
		return va, nil

	case argDeref:
		src, st, err := c.pathValue(slots, env, a.path)
		if err != nil {
			return nil, fmt.Errorf("compile: %s argument %d: %w", name, pos+1, err)
		}
		da, dt, err := derefArg(src, st, joinPath(a.path))
		if err != nil {
			return nil, fmt.Errorf("compile: %s argument %d: %w", name, pos+1, err)
		}
		if !dt.AssignableTo(pt) {
			return nil, fmt.Errorf("compile: %s argument %d: cannot use %s as %s", name, pos+1, dt, pt)
		}
		da.typ = pt
		return da, nil

	case argPath:
		slot, ok := slots[a.path[0]]
		if !ok {
			// Not a program name: the path may still name a value
			// binding, which owns the whole dotted prefix the way a
			// func binding does.
			va, vt, found, err := c.varArg(a.path, false)
			if err != nil {
				return nil, fmt.Errorf("compile: %s argument %d: %w", name, pos+1, err)
			}
			if found {
				if !vt.AssignableTo(pt) {
					return nil, fmt.Errorf("compile: %s argument %d: cannot use %s as %s", name, pos+1, vt, pt)
				}
				va.typ = pt
				return va, nil
			}
			return nil, fmt.Errorf("compile: %s argument %d: %s is not a name bound by the program, so its fields are unknown", name, pos+1, a.path[0])
		}
		cur := &vmArg{kind: vaSlot, slot: slot, name: a.path[0], typ: env[a.path[0]], iface: -1}
		curType := env[a.path[0]]
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

	case argSlice:
		sa, st, err := c.compileSliceLit(slots, env, a)
		if err != nil {
			return nil, fmt.Errorf("compile: %s argument %d: %w", name, pos+1, err)
		}
		if !st.AssignableTo(pt) {
			return nil, fmt.Errorf("compile: %s argument %d: cannot use %s as %s", name, pos+1, st, pt)
		}
		sa.typ = pt
		return sa, nil

	case argStruct:
		sa, st, err := c.compileStructLit(slots, env, a)
		if err != nil {
			return nil, fmt.Errorf("compile: %s argument %d: %w", name, pos+1, err)
		}
		if !st.AssignableTo(pt) {
			return nil, fmt.Errorf("compile: %s argument %d: cannot use %s as %s", name, pos+1, st, pt)
		}
		sa.typ = pt
		return sa, nil

	case argVar:
		if a.str == "dest" {
			return &vmArg{kind: vaDest, name: "dest", typ: pt, iface: -1}, nil
		}
		if va, vt, found, err := c.varArg([]string{a.str}, false); found || err != nil {
			if err != nil {
				return nil, fmt.Errorf("compile: %s argument %d: %w", name, pos+1, err)
			}
			if !vt.AssignableTo(pt) {
				return nil, fmt.Errorf("compile: %s argument %d: cannot use %s as %s", name, pos+1, vt, pt)
			}
			va.typ = pt
			return va, nil
		}
		if slot, ok := slots[a.str]; ok {
			st := env[a.str]
			if st != nil && !st.AssignableTo(pt) {
				return nil, fmt.Errorf("compile: %s argument %d: cannot use %s as %s", name, pos+1, st, pt)
			}
			return &vmArg{kind: vaSlot, slot: slot, name: a.str, typ: pt, iface: -1}, nil
		}
		// Because the program does not bind the name, the value comes
		// off the caller's stack and its type stays unknown until the
		// VM reads it.
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
