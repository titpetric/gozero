package gozero

import (
	"fmt"
	"reflect"
	"strconv"
)

// The operand rules of the expression grammar. An operand is a name
// bound by the program or a literal, nothing that could call, read a
// field, build a value or block; the admission tables below say which
// kinds each operator class accepts. vm_expr.go types the tree,
// vm_fold.go folds the constant subtrees, vm_ops.go evaluates.

// spellExpr spells an expression back the way the source wrote it,
// for error messages. Nested binary operands are parenthesized, which
// keeps the spelling unambiguous without tracking the source span.
func spellExpr(a arg) string {
	switch a.kind {
	case argBinary:
		return spellOperand(*a.x) + " " + a.op + " " + spellOperand(*a.y)
	case argUnary:
		return a.op + spellOperand(*a.x)
	}
	return spellArg(a)
}

func spellOperand(a arg) string {
	if a.kind == argBinary {
		return "(" + spellExpr(a) + ")"
	}
	return spellExpr(a)
}

// spellArg spells one operand back the way the source wrote it, for
// error messages.
func spellArg(a arg) string {
	switch a.kind {
	case argString:
		return strconv.Quote(a.str)
	case argInt:
		return strconv.FormatInt(a.i, 10)
	case argFloat:
		return strconv.FormatFloat(a.f, 'g', -1, 64)
	case argBool:
		return strconv.FormatBool(a.b)
	case argVar:
		return a.str
	case argPath:
		return joinPath(a.path)
	case argNil:
		return "nil"
	case argCall:
		return joinPath(a.sub.path) + "(...)"
	case argStruct:
		return joinPath(a.path) + "{...}"
	case argRecv:
		return "<-" + spellArg(*a.recv)
	}
	return "a value"
}

// addsAt reports whether + is defined at a kind: strings, integers
// and floats. Go also adds complex numbers; no layout class carries
// one, so they stay rejected by the same rule that rejects them for
// ++ and --.
func addsAt(k reflect.Kind) bool {
	return k == reflect.String || stepsAt(k)
}

// comparesAt reports the kinds == and != cover here: the built-in
// comparable scalars and strings. Go compares more - pointers,
// channels, interfaces, comparable structs and arrays - and an
// interface comparison can panic at run time; all of those stay
// rejected, so equality never adds a panic site to the language.
func comparesAt(k reflect.Kind) bool {
	return k == reflect.Bool || k == reflect.String || stepsAt(k)
}

// binopOperand resolves one operand leaf. A bound name comes back as
// its slot with its static type; a literal comes back nil, typed by
// the caller once the other side's type is known. Everything else is
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

// binopLiteral converts a literal operand to the named side's type.
// It exists beside literalValue because Go's untyped constants
// convert to named string and bool types, which AssignableTo does not
// cover; the numeric kinds go through the same representability
// checks every literal argument gets.
func binopLiteral(t reflect.Type, a arg) (reflect.Value, error) {
	switch t.Kind() {
	case reflect.String:
		if a.kind != argString {
			return reflect.Value{}, fmt.Errorf("cannot use %s as %s", spellArg(a), t)
		}
		v := reflect.New(t).Elem()
		v.SetString(a.str)
		return v, nil
	case reflect.Bool:
		if a.kind != argBool {
			return reflect.Value{}, fmt.Errorf("cannot use %s as %s", spellArg(a), t)
		}
		v := reflect.New(t).Elem()
		v.SetBool(a.b)
		return v, nil
	}
	if a.kind == argString || a.kind == argBool {
		return reflect.Value{}, fmt.Errorf("cannot use %s as %s", spellArg(a), t)
	}
	return literalAs(t, a)
}
