package gozero

import (
	"fmt"
	"reflect"
)

// The operator assignment, "s := a + b;". One binary operator per
// statement is the whole expression grammar: + concatenates strings
// and adds integers and floats, == and != compare booleans, integers,
// floats and strings. The typing rule is one line of Go's: operands
// need identical static types, and a literal side adopts the named
// side's type, checked for representability at compile time. Two
// literal operands are rejected rather than folded, because folding
// is go/constant's job and reimplementing its arithmetic is exactly
// what this design refuses.
//
// == and != are the header comparison's own operators, so the
// comparison half of the statement reuses the header kit: cmpClassOf
// admits the kind and cmpEval runs the compare (vm_cmp.go). Only the
// sums below are this file's own semantics.

// vmBinop is a compiled operator assignment: the operands, the
// operator, and the type the operation runs at. An operand is vaSlot
// or vaConst, never anything that could call or allocate.
type vmBinop struct {
	op    string       // "+", "==" or "!="
	x, y  *vmArg       // vaSlot or vaConst
	t     reflect.Type // the identical operand type
	class cmpClass     // the compare class of == and !=, unset for +
	name  string       // "a + b", for diagnostics
}

// resultType is the static type of the assigned name: the operand
// type for +, the predeclared bool for a comparison, as in Go.
func (b *vmBinop) resultType() reflect.Type {
	if b.op == "+" {
		return b.t
	}
	return reflect.TypeFor[bool]()
}

// addsAt reports whether + is defined at a kind: strings, integers
// and floats. Go also adds complex numbers; no layout class carries
// one, so they stay rejected by the same rule that rejects them for
// ++ and --.
func addsAt(k reflect.Kind) bool {
	return k == reflect.String || stepsAt(k)
}

// compileBinopStmt compiles the whole statement: the declaration
// rules every assignment obeys, the operator itself, and the slot
// the result binds to.
func (pc *progCompiler) compileBinopStmt(s stmt) (vmStmt, error) {
	if len(s.lhs) != 1 {
		return vmStmt{}, fmt.Errorf("compile: an operator assigns to exactly one name")
	}
	name := s.lhs[0]
	if err := pc.checkName(name); err != nil {
		return vmStmt{}, err
	}
	if err := pc.checkDecl(name, s.define); err != nil {
		return vmStmt{}, err
	}
	if s.define {
		if err := pc.checkNew(s.lhs); err != nil {
			return vmStmt{}, err
		}
	}
	bn, err := pc.c.compileBinop(pc.slots, pc.env, s)
	if err != nil {
		return vmStmt{}, err
	}
	// The result assigns at its own type, so both tiers store into a
	// slot whose layout is the operator's: a comparison into an
	// interface slot would need a conversion neither tier compiles
	// here.
	rt := bn.resultType()
	if prev, ok := pc.env[name]; ok && prev != rt {
		return vmStmt{}, fmt.Errorf("compile: %s: cannot use %s as %s", name, rt, prev)
	}
	return vmStmt{binop: bn, out: []int{pc.newSlot(name, rt)}}, nil
}

// compileBinop compiles "s := x op y". Both operands resolve here: a
// name to its slot and static type, a literal to a constant at the
// other side's type.
func (c *Compiler) compileBinop(slots map[string]int, env map[string]reflect.Type, s stmt) (*vmBinop, error) {
	expr := spellArg(*s.binX) + " " + s.binOp + " " + spellArg(*s.binY)
	xa, xt, err := c.binopOperand(slots, env, expr, *s.binX)
	if err != nil {
		return nil, err
	}
	ya, yt, err := c.binopOperand(slots, env, expr, *s.binY)
	if err != nil {
		return nil, err
	}
	var t reflect.Type
	switch {
	case xa == nil && ya == nil:
		return nil, fmt.Errorf("compile: %s: both operands are literals, write the value it folds to", expr)
	case xa == nil:
		t = yt
		v, err := adoptLiteral(t, *s.binX)
		if err != nil {
			return nil, fmt.Errorf("compile: %s: %w", expr, err)
		}
		xa = &vmArg{kind: vaConst, val: v, typ: t, iface: -1}
	case ya == nil:
		t = xt
		v, err := adoptLiteral(t, *s.binY)
		if err != nil {
			return nil, fmt.Errorf("compile: %s: %w", expr, err)
		}
		ya = &vmArg{kind: vaConst, val: v, typ: t, iface: -1}
	default:
		if xt != yt {
			return nil, fmt.Errorf("compile: %s: invalid operation: mismatched types %s and %s", expr, xt, yt)
		}
		t = xt
	}
	if t == nil {
		return nil, fmt.Errorf("compile: %s: an operand has no type", expr)
	}
	b := &vmBinop{op: s.binOp, x: xa, y: ya, t: t, name: expr}
	if s.binOp == "+" {
		if !addsAt(t.Kind()) {
			return nil, fmt.Errorf("compile: %s: operator + is not defined on %s, it concatenates strings and adds integers and floats", expr, t)
		}
		return b, nil
	}
	// The header's own admission rule, which covers exactly the
	// comparable scalars and strings. Go compares more - pointers,
	// channels, interfaces, comparable structs and arrays - and an
	// interface comparison can panic at run time; all of those stay
	// rejected, so no operator adds a panic site to the language.
	class, ok := cmpClassOf(t.Kind(), s.binOp)
	if !ok {
		return nil, fmt.Errorf("compile: %s: == and != compare booleans, integers, floats and strings, not %s", expr, t)
	}
	b.class = class
	return b, nil
}

// binopOperand resolves one operand. A bound name comes back as its
// slot with its static type; a literal comes back nil, typed by the
// caller once the other side's type is known. Everything else is
// rejected by rule: an operand never calls, reads a field, builds a
// value or blocks.
func (c *Compiler) binopOperand(slots map[string]int, env map[string]reflect.Type, expr string, a arg) (*vmArg, reflect.Type, error) {
	switch a.kind {
	case argVar:
		if slot, ok := slots[a.str]; ok {
			t := env[a.str]
			return &vmArg{kind: vaSlot, slot: slot, name: a.str, typ: t, iface: -1}, t, nil
		}
		return nil, nil, fmt.Errorf("compile: %s: %s has no static type here; a name read from the stack cannot be an operand, bind it with := first", expr, a.str)
	case argString, argInt, argFloat, argBool:
		return nil, nil, nil
	}
	return nil, nil, fmt.Errorf("compile: %s: %s cannot be an operand, only a name bound by the program or a literal can", expr, spellArg(a))
}

// operand reads one operand's current value.
func (b *vmBinop) operand(slots []reflect.Value, a *vmArg) reflect.Value {
	if a.kind == vaConst {
		return a.val
	}
	v := slots[a.slot]
	if !v.IsValid() {
		return reflect.Zero(a.typ)
	}
	return v
}

// exec computes the statement's value. A sum mirrors what compiled
// Go does at the type's width: an integer wraps by masking unsigned
// bits or shift-truncating signed ones, the way a step does in
// vm_inc.go, and a float32 sum rounds once at 32 bits, where an
// overflow becomes the infinity the native operation produces. A
// comparison is the header's own, NaN included.
func (b *vmBinop) exec(slots []reflect.Value) (reflect.Value, error) {
	x := b.operand(slots, b.x)
	y := b.operand(slots, b.y)
	if b.op != "+" {
		return reflect.ValueOf(cmpEval(b.op, b.class, x, y)), nil
	}
	out := reflect.New(b.t).Elem()
	switch b.t.Kind() {
	case reflect.String:
		out.SetString(x.String() + y.String())
	case reflect.Float32, reflect.Float64:
		sum := x.Float() + y.Float()
		if b.t.Kind() == reflect.Float32 {
			// The float64 sum of two float32 values is exact enough
			// that rounding it once to 32 bits is the rounding
			// compiled Go performs; SetFloat then sees a value that is
			// either representable or an infinity.
			sum = float64(float32(sum))
		}
		out.SetFloat(sum)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := x.Uint() + y.Uint()
		if bits := b.t.Bits(); bits < 64 {
			u &= 1<<bits - 1
		}
		out.SetUint(u)
	default:
		n := x.Int() + y.Int()
		if bits := b.t.Bits(); bits < 64 {
			sh := 64 - bits
			n = n << sh >> sh
		}
		out.SetInt(n)
	}
	return out, nil
}
