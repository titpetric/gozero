package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// Expression compilation. Typing is a deliberately small slice of
// Go's rules: operands of a binary operator must have identical
// static types, a literal operand adopts the other side's type when
// representable, and an all-literal expression folds at compile time.
// The evaluator closures are chosen at compile time per operator and
// operand kind, and their bodies use Go's own operators, so
// wraparound, division panics and shift behaviour are native by
// construction; both tiers share them.

var (
	boolType = reflect.TypeFor[bool]()
	intType  = reflect.TypeFor[int]()
)

// isExprKind reports the argument kinds the expression compiler owns.
func isExprKind(k argKind) bool {
	return k == argBinary || k == argUnary || k == argIndex
}

// isLenCall reports a call of the len builtin: the one-segment path
// len with no method chain, when nothing rebinds the name.
func (c *Compiler) isLenCall(sc *cscope, e *callExpr) bool {
	if len(e.path) != 1 || e.path[0] != "len" || len(e.chain) != 0 {
		return false
	}
	_, bound := sc.bindings["len"]
	return !bound
}

// compileLen compiles len(x) for a slice, array, string, map or
// channel operand. The result is int, as in Go.
func (c *Compiler) compileLen(sc *cscope, e *callExpr) (*vmArg, reflect.Type, error) {
	if len(e.args) != 1 {
		return nil, nil, fmt.Errorf("compile: len takes exactly one argument, got %d", len(e.args))
	}
	x, xt, err := c.compileValueExpr(sc, e.args[0], nil)
	if err != nil {
		return nil, nil, err
	}
	switch xt.Kind() {
	case reflect.Slice, reflect.Array, reflect.String, reflect.Map, reflect.Chan:
	default:
		return nil, nil, fmt.Errorf("compile: invalid argument: len of %s", sc.typeName(xt))
	}
	return &vmArg{kind: vaLen, x: x, typ: intType, iface: -1}, intType, nil
}

// compileValueExpr compiles an expression bottom-up and reports its
// static type. want, when non-nil and concrete, is the type a folded
// constant lands as; a non-constant expression's type is its own and
// the caller checks assignability.
func (c *Compiler) compileValueExpr(sc *cscope, a arg, want reflect.Type) (*vmArg, reflect.Type, error) {
	if lit, ok, err := foldExpr(a); err != nil {
		return nil, nil, err
	} else if ok {
		t := want
		if t == nil || t.Kind() == reflect.Interface {
			t = defaultLitType(lit)
		}
		v, err := literalValue(t, lit)
		if err != nil {
			return nil, nil, fmt.Errorf("compile: %w", err)
		}
		return &vmArg{kind: vaConst, val: v, typ: v.Type(), iface: -1}, v.Type(), nil
	}
	switch a.kind {
	case argBinary:
		return c.compileBinary(sc, a)
	case argUnary:
		return c.compileUnary(sc, a)
	case argIndex:
		return c.compileIndex(sc, a)
	default:
		return c.compileOperand(sc, a)
	}
}

// compileOperand compiles a typed leaf of an expression: a name, a
// field read, or a call. Stack names carry no static type, which
// operators need, so they are rejected with the workaround named.
func (c *Compiler) compileOperand(sc *cscope, a arg) (*vmArg, reflect.Type, error) {
	switch a.kind {
	case argVar:
		if slot, ok := sc.slots[a.str]; ok {
			t := sc.env[a.str]
			return &vmArg{kind: vaSlot, slot: slot, name: a.str, typ: t, iface: -1}, t, nil
		}
		return nil, nil, fmt.Errorf("compile: %s has no static type here; a name read from the stack cannot be an operand, bind it with := first", a.str)
	case argPath:
		slot, ok := sc.slots[a.path[0]]
		if !ok {
			return nil, nil, fmt.Errorf("compile: %s is not a name bound by the program, so its fields cannot be operands", a.path[0])
		}
		cur := &vmArg{kind: vaSlot, slot: slot, name: a.path[0], typ: sc.env[a.path[0]], iface: -1}
		curType := sc.env[a.path[0]]
		for _, seg := range a.path[1:] {
			f, deref, ok := fieldOf(curType, seg)
			if !ok {
				return nil, nil, fmt.Errorf("compile: %s has no field %s", sc.typeName(curType), seg)
			}
			cur = &vmArg{kind: vaField, src: cur, index: f.Index, deref: deref, typ: f.Type, iface: -1}
			curType = f.Type
		}
		return cur, curType, nil
	case argCall:
		if c.isLenCall(sc, a.sub) {
			return c.compileLen(sc, a.sub)
		}
		sub, st, err := c.compileExpr(sc, a.sub)
		if err != nil {
			return nil, nil, err
		}
		if sub.nres == 0 || st == nil {
			return nil, nil, fmt.Errorf("compile: %s returns no value", sub.name)
		}
		return &vmArg{kind: vaCall, sub: sub, typ: st, iface: -1}, st, nil
	case argBinary, argUnary, argIndex:
		return c.compileValueExpr(sc, a, nil)
	case argNil:
		return nil, nil, fmt.Errorf("compile: nil can only be compared with == or !=")
	default:
		return nil, nil, fmt.Errorf("compile: this value cannot be an operand")
	}
}

func (c *Compiler) compileBinary(sc *cscope, a arg) (*vmArg, reflect.Type, error) {
	op := a.op

	// x == nil and x != nil test any nilable operand directly.
	if (op == "==" || op == "!=") && (a.x.kind == argNil || a.y.kind == argNil) {
		opnd := a.x
		if a.x.kind == argNil {
			opnd = a.y
		}
		if opnd.kind == argNil {
			return nil, nil, fmt.Errorf("compile: invalid operation: nil %s nil", op)
		}
		x, xt, err := c.compileValueExpr(sc, *opnd, nil)
		if err != nil {
			return nil, nil, err
		}
		switch xt.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		default:
			return nil, nil, fmt.Errorf("compile: invalid operation: %s %s nil (%s is not nilable)", op, op, sc.typeName(xt))
		}
		node := &vmArg{kind: vaBinary, op: op + "nil", x: x, typ: boolType, iface: -1}
		return node, boolType, nil
	}

	// Short-circuit logic is control flow, evaluated by get itself.
	if op == "&&" || op == "||" {
		x, xt, err := c.compileValueExpr(sc, *a.x, boolType)
		if err != nil {
			return nil, nil, err
		}
		y, yt, err := c.compileValueExpr(sc, *a.y, boolType)
		if err != nil {
			return nil, nil, err
		}
		if xt.Kind() != reflect.Bool || yt.Kind() != reflect.Bool {
			return nil, nil, fmt.Errorf("compile: invalid operation: %s wants bool operands, got %s and %s", op, sc.typeName(xt), sc.typeName(yt))
		}
		return &vmArg{kind: vaBinary, op: op, x: x, y: y, typ: boolType, iface: -1}, boolType, nil
	}

	// A shift's count may be any integer type; everything else wants
	// identical operand types, with a literal side adopting the other.
	shift := op == "<<" || op == ">>"
	fx, xConst, err := foldExpr(*a.x)
	if err != nil {
		return nil, nil, err
	}
	fy, yConst, err := foldExpr(*a.y)
	if err != nil {
		return nil, nil, err
	}
	var x, y *vmArg
	var xt, yt reflect.Type
	switch {
	case yConst:
		x, xt, err = c.compileValueExpr(sc, *a.x, nil)
		if err != nil {
			return nil, nil, err
		}
		lt := xt
		if shift {
			lt = intType
		}
		v, lerr := literalValue(lt, fy)
		if lerr != nil {
			return nil, nil, fmt.Errorf("compile: %w", lerr)
		}
		y, yt = &vmArg{kind: vaConst, val: v, typ: v.Type(), iface: -1}, v.Type()
	case xConst:
		y, yt, err = c.compileValueExpr(sc, *a.y, nil)
		if err != nil {
			return nil, nil, err
		}
		lt := yt
		if shift {
			// The count is typed; the shifted literal defaults.
			lt = defaultLitType(fx)
		}
		v, lerr := literalValue(lt, fx)
		if lerr != nil {
			return nil, nil, fmt.Errorf("compile: %w", lerr)
		}
		x, xt = &vmArg{kind: vaConst, val: v, typ: v.Type(), iface: -1}, v.Type()
	default:
		x, xt, err = c.compileValueExpr(sc, *a.x, nil)
		if err != nil {
			return nil, nil, err
		}
		y, yt, err = c.compileValueExpr(sc, *a.y, nil)
		if err != nil {
			return nil, nil, err
		}
	}
	if !shift && xt != yt {
		return nil, nil, fmt.Errorf("compile: invalid operation: mismatched types %s and %s", sc.typeName(xt), sc.typeName(yt))
	}
	if shift && !isIntKind(yt.Kind()) {
		return nil, nil, fmt.Errorf("compile: invalid operation: shift count type %s, must be integer", sc.typeName(yt))
	}

	rt, err := binResultType(op, xt)
	if err != nil {
		return nil, nil, fmt.Errorf("compile: invalid operation: %w", err)
	}
	fn := binEval(op, xt)
	if fn == nil {
		return nil, nil, fmt.Errorf("compile: invalid operation: %s is not defined on %s", op, sc.typeName(xt))
	}
	return &vmArg{kind: vaBinary, op: op, x: x, y: y, binFn: fn, typ: rt, iface: -1}, rt, nil
}

func (c *Compiler) compileUnary(sc *cscope, a arg) (*vmArg, reflect.Type, error) {
	x, xt, err := c.compileValueExpr(sc, *a.x, nil)
	if err != nil {
		return nil, nil, err
	}
	fn := unEval(a.op, xt)
	if fn == nil {
		return nil, nil, fmt.Errorf("compile: invalid operation: %s is not defined on %s", a.op, sc.typeName(xt))
	}
	return &vmArg{kind: vaUnary, op: a.op, x: x, unFn: fn, typ: xt, iface: -1}, xt, nil
}

func (c *Compiler) compileIndex(sc *cscope, a arg) (*vmArg, reflect.Type, error) {
	x, xt, err := c.compileValueExpr(sc, *a.x, nil)
	if err != nil {
		return nil, nil, err
	}
	var elem, keyWant reflect.Type
	switch xt.Kind() {
	case reflect.Slice, reflect.Array:
		elem, keyWant = xt.Elem(), intType
	case reflect.String:
		elem, keyWant = reflect.TypeFor[byte](), intType
	case reflect.Map:
		elem, keyWant = xt.Elem(), xt.Key()
	default:
		return nil, nil, fmt.Errorf("compile: invalid operation: %s is not indexable", sc.typeName(xt))
	}
	y, yt, err := c.compileValueExpr(sc, *a.y, keyWant)
	if err != nil {
		return nil, nil, err
	}
	if xt.Kind() == reflect.Map {
		if !yt.AssignableTo(keyWant) {
			return nil, nil, fmt.Errorf("compile: cannot use %s as the %s key of %s", sc.typeName(yt), sc.typeName(keyWant), sc.typeName(xt))
		}
	} else if !isIntKind(yt.Kind()) {
		return nil, nil, fmt.Errorf("compile: index of type %s, must be integer", sc.typeName(yt))
	}
	return &vmArg{kind: vaIndex, x: x, y: y, typ: elem, iface: -1}, elem, nil
}

func isIntKind(k reflect.Kind) bool {
	return k >= reflect.Int && k <= reflect.Uintptr
}

// defaultLitType is the width the parser gave a literal, when no use
// fixes another.
func defaultLitType(a arg) reflect.Type {
	switch a.kind {
	case argFloat:
		return reflect.TypeFor[float64]()
	case argString:
		return reflect.TypeFor[string]()
	case argBool:
		return boolType
	}
	return reflect.TypeFor[int64]()
}

// binResultType is the type an operator produces over operands of t.
func binResultType(op string, t reflect.Type) (reflect.Type, error) {
	switch op {
	case "==", "!=", "<", "<=", ">", ">=":
		return boolType, nil
	}
	return t, nil
}

// evalBinary evaluates a vaBinary node: the short-circuit ops and nil
// comparisons inline, everything else through the compile-time chosen
// closure.
func (a *vmArg) evalBinary(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) (reflect.Value, error) {
	switch a.op {
	case "&&", "||":
		xv, err := a.x.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return reflect.Value{}, err
		}
		// The right side only runs when the left does not decide, as
		// in Go.
		if a.op == "&&" && !xv.Bool() {
			return reflect.ValueOf(false), nil
		}
		if a.op == "||" && xv.Bool() {
			return reflect.ValueOf(true), nil
		}
		return a.y.get(ctx, slots, frame, ifaces, stack, dest)
	case "==nil", "!=nil":
		xv, err := a.x.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return reflect.Value{}, err
		}
		isNil := !xv.IsValid() || xv.IsNil()
		if a.op == "!=nil" {
			isNil = !isNil
		}
		return reflect.ValueOf(isNil), nil
	}
	xv, err := a.x.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return reflect.Value{}, err
	}
	yv, err := a.y.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return reflect.Value{}, err
	}
	return a.binFn(xv, yv), nil
}

// evalIndex evaluates x[y]. A missing map key reads as the element's
// zero value, the one-value form of Go's map index; slice, array and
// string indexing bounds-check inside reflect, and the panic arrives
// as *PanicError through the guard, as it would from compiled Go.
func (a *vmArg) evalIndex(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) (reflect.Value, error) {
	xv, err := a.x.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return reflect.Value{}, err
	}
	yv, err := a.y.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return reflect.Value{}, err
	}
	if xv.Kind() == reflect.Map {
		v := xv.MapIndex(yv)
		if !v.IsValid() {
			return reflect.Zero(a.typ), nil
		}
		return v, nil
	}
	var i int
	if yv.CanUint() {
		i = int(yv.Uint())
	} else {
		i = int(yv.Int())
	}
	return xv.Index(i), nil
}
