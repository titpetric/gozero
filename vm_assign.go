package gozero

import (
	"fmt"
	"reflect"
)

// The assignment statements that are not a call: a field write and
// the value form of return. Both resolve their target first and then
// compile the value against the target's type, which is what keeps a
// literal's width and a name's static type honest.

// compileFieldSet compiles req.Method = value. The base is a
// program-bound name, every selector is an exported field, and the
// value is a literal or a call whose result is assignable to the field.
func (c *Compiler) compileFieldSet(slots map[string]int, env map[string]reflect.Type, s stmt) (*vmFieldSet, error) {
	base := s.fieldLhs[0]
	slot, ok := slots[base]
	if !ok {
		return nil, fmt.Errorf("compile: %s is not a name bound by the program, so its fields cannot be assigned", base)
	}
	t := env[base]
	fs := &vmFieldSet{base: slot, field: joinPath(s.fieldLhs)}
	for _, seg := range s.fieldLhs[1:] {
		f, deref, ok := fieldOf(t, seg)
		if !ok {
			return nil, fmt.Errorf("compile: %s has no field %s", t, seg)
		}
		fs.steps = append(fs.steps, fieldStep{index: f.Index, deref: deref})
		t = f.Type
	}

	val, err := c.assignedValue(slots, env, fs.field, t, s)
	if err != nil {
		return nil, err
	}
	fs.val = val
	return fs, nil
}

// compileRetVal compiles the value of a "return x;" form. The
// parameter type it is compiled against is its own: a program-bound
// name uses its static type, a stack name has none and is returned as
// it is, a literal keeps its natural width.
func (c *Compiler) compileRetVal(slots map[string]int, env map[string]reflect.Type, a arg) (*vmArg, error) {
	pt := reflect.TypeFor[any]()
	switch a.kind {
	case argVar:
		if t, ok := env[a.str]; ok && t != nil {
			pt = t
		}
	case argString:
		pt = reflect.TypeFor[string]()
	case argInt:
		pt = reflect.TypeFor[int64]()
	case argFloat:
		pt = reflect.TypeFor[float64]()
	case argBool:
		pt = reflect.TypeFor[bool]()
	case argNil:
		return nil, fmt.Errorf("compile: return nil returns no value, use return;")
	case argPath, argStruct:
		// compileArg resolves these and checks assignability against
		// pt, so any is what lets the value keep its own type.
	}
	return c.compileArg(slots, env, "return", 0, pt, a)
}
