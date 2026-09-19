package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// Func literals on the direct tier. The literal is capture-free, so
// its value is a compile-time constant; what varies is what the
// constant is. When the body itself JITs and the signature's layout is
// in the closure table below, the constant is an ordinary Go closure
// of the shape type over the body's node list, reinterpreted as the
// target func type the way castFn reinterprets a binding: no
// reflect.MakeFunc anywhere in the call path. Any other signature
// keeps the MakeFunc value the program compiler built and is recorded
// as bridged, exactly as an out-of-table call is.

// funcLitNode compiles a func literal into the node yielding its
// funcval pointer. A capture-free literal's pointer is constant and
// the closure it names stays reachable through the node; a capturing
// literal's closure closes over this run's frame and is built per
// evaluation, in funcLitCapNode.
func (c *jitCompiler) funcLitNode(a *vmArg) (node, error) {
	fl := a.funclit
	if len(fl.capOuter) > 0 {
		return c.funcLitCapNode(fl)
	}
	fn, bridged, why := funcLitClosure(fl)
	switch {
	case fn == nil:
		c.bridged = append(c.bridged, fmt.Sprintf("%s: func literal (%s)", fl.name, why))
		fn = a.val.Interface()
	case len(bridged) > 0:
		for _, b := range bridged {
			c.bridged = append(c.bridged, fmt.Sprintf("%s: func literal body: %s", fl.name, b))
		}
	}
	keep := fn
	ptr := funcPtr(keep)
	return node{class: lPtr, P: func(unsafe.Pointer, context.Context, map[string]any, any) (unsafe.Pointer, error) {
		// keep pins the funcval ptr refers to for the program's life.
		_ = keep
		return ptr, nil
	}}, nil
}

// funcLitCapNode compiles a capturing literal. The captured cells are
// this run's frame slots, so the frame escapes into the closure and
// the program's frame pool turns off for good: that is the rung's
// stated cost, one frame allocation per run that pooling previously
// removed. The closure itself is one more allocation per run, the
// same one the Go compiler emits for an escaping literal.
//
// When the body lowers and the signature is in the closure table, the
// per-run value is an ordinary Go closure over the capBody and the
// frame pointer: no reflect anywhere. Otherwise the literal keeps the
// MakeFunc materialization, its cells built with reflect.NewAt over
// the same frame slots, so capture stays write-through across the
// bridge.
func (c *jitCompiler) funcLitCapNode(fl *vmFuncLit) (node, error) {
	c.frameEscapes = true
	offs := make([]uintptr, len(fl.capOuter))
	for i, slot := range fl.capOuter {
		field, ok := c.slotOf[slot]
		if !ok {
			// The captured name has no frame field here: it is itself a
			// capture of a farther scope, which one capBase hop cannot
			// address. The program declines and the reflect evaluator
			// keeps the chain.
			return node{}, fmt.Errorf("%s: a capture of a captured name stays on the reflect tier", fl.name)
		}
		offs[i] = c.offs[field]
	}

	capOffs := map[int]uintptr{}
	for i, slot := range fl.capLocal {
		capOffs[slot] = offs[i]
	}
	cb, bridged, why := capLitClosure(capOffs, fl)
	if cb != nil {
		for _, b := range bridged {
			c.bridged = append(c.bridged, fmt.Sprintf("%s: func literal body: %s", fl.name, b))
		}
		return node{class: lPtr, P: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (unsafe.Pointer, error) {
			return funcPtr(capClosure(fr, cb)), nil
		}}, nil
	}

	c.bridged = append(c.bridged, fmt.Sprintf("%s: func literal (%s)", fl.name, why))
	types, body, plan := fl.capTypes, fl.body, fl.plan
	return node{class: lPtr, P: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (unsafe.Pointer, error) {
		env := make([]reflect.Value, len(offs))
		for i, off := range offs {
			env[i] = reflect.NewAt(types[i], unsafe.Add(fr, off)).Elem()
		}
		fv := plan.bind(func(ctx context.Context, args []reflect.Value) (any, error) {
			return body.runWithEnv(ctx, args, env, nil, nil)
		})
		return funcPtr(fv.Interface()), nil
	}}, nil
}

// capLitClosure builds the compile-time half of the direct capturing
// closure, or reports why the literal stays on the MakeFunc bridge.
func capLitClosure(capOffs map[int]uintptr, fl *vmFuncLit) (cb *capBody, bridged []string, why string) {
	ft := fl.ft
	if ft.NumOut() > 0 {
		return nil, nil, "a result stays on the MakeFunc bridge"
	}
	for i := 0; i < ft.NumIn(); i++ {
		if ft.In(i) == ctxType {
			return nil, nil, "a context.Context parameter stays on the MakeFunc bridge"
		}
	}
	key := ""
	for i := 0; i < ft.NumIn(); i++ {
		key += layoutOf(ft.In(i)).String()
	}
	key += "_"
	jp, err := jitCompileWith(capOffs, fl.body)
	if err != nil {
		return nil, nil, fmt.Sprintf("the body stays on the reflect tier: %v", err)
	}
	if capClosure(nil, &capBody{jp: jp, key: key}) == nil {
		return nil, nil, fmt.Sprintf("signature shape %q is not in the closure table", key)
	}
	return &capBody{jp: jp, key: key}, jp.bridged, ""
}

// funcLitClosure builds the direct closure over the body, or reports
// why the literal stays on the MakeFunc bridge. bridged carries the
// body's own bridged calls when the closure is built, so Supports
// still names every call that pays reflect.
func funcLitClosure(fl *vmFuncLit) (fn any, bridged []string, why string) {
	ft := fl.ft
	if ft.NumOut() > 0 {
		return nil, nil, "a result stays on the MakeFunc bridge"
	}
	for i := 0; i < ft.NumIn(); i++ {
		if ft.In(i) == ctxType {
			return nil, nil, "a context.Context parameter stays on the MakeFunc bridge"
		}
	}
	key := ""
	for i := 0; i < ft.NumIn(); i++ {
		key += layoutOf(ft.In(i)).String()
	}
	key += "_"
	jp, err := jitCompileProgram(fl.body)
	if err != nil {
		return nil, nil, fmt.Sprintf("the body stays on the reflect tier: %v", err)
	}
	cl := bodyClosure(key, jp)
	if cl == nil {
		return nil, nil, fmt.Sprintf("signature shape %q is not in the closure table", key)
	}
	return cl, jp.bridged, ""
}

// bodyClosure is the closure table: one case per signature layout,
// each an ordinary Go closure over the body program. A closure takes
// a frame, stores the incoming parameter words into the body's slots
// and runs the node list; the parameter types in the frame are the
// signature's own, so the stores are plain word copies with no
// conversion. Only signatures without results are here: an error has
// no slot to travel in, so a failing body panics, which is what the
// MakeFunc side does when the signature has no error result.
func bodyClosure(key string, jp *jitProgram) any {
	switch key {
	case "_":
		return func() {
			jp.runBody(jp.newFrame())
		}
	case "P_":
		return func(a0 unsafe.Pointer) {
			f := jp.newFrame()
			*(*unsafe.Pointer)(unsafe.Add(f, jp.paramOffs[0])) = a0
			jp.runBody(f)
		}
	case "S_":
		return func(a0 string) {
			f := jp.newFrame()
			*(*string)(unsafe.Add(f, jp.paramOffs[0])) = a0
			jp.runBody(f)
		}
	case "I_":
		return func(a0 ifacePair) {
			f := jp.newFrame()
			*(*ifacePair)(unsafe.Add(f, jp.paramOffs[0])) = a0
			jp.runBody(f)
		}
	case "II_":
		return func(a0, a1 ifacePair) {
			f := jp.newFrame()
			*(*ifacePair)(unsafe.Add(f, jp.paramOffs[0])) = a0
			*(*ifacePair)(unsafe.Add(f, jp.paramOffs[1])) = a1
			jp.runBody(f)
		}
	case "IP_":
		// func(http.ResponseWriter, *http.Request), the handler shape.
		return func(a0 ifacePair, a1 unsafe.Pointer) {
			f := jp.newFrame()
			*(*ifacePair)(unsafe.Add(f, jp.paramOffs[0])) = a0
			*(*unsafe.Pointer)(unsafe.Add(f, jp.paramOffs[1])) = a1
			jp.runBody(f)
		}
	}
	return nil
}

// newFrame is getFrame for a body invoked from outside any run; a
// body with no slots runs on a nil frame like any program.
func (p *jitProgram) newFrame() unsafe.Pointer {
	if p.frameRT == nil {
		return nil
	}
	return p.getFrame()
}

// runBody drains the node list over f and repools. The execution
// context is Background: the literal is capture-free, the closure may
// outlive the defining run, and it inherits nothing from it. An error
// panics; only signatures without an error result reach this runner.
func (p *jitProgram) runBody(f unsafe.Pointer) {
	for _, st := range p.stmts {
		if err := st(f, context.Background(), nil, nil); err != nil {
			p.finish(f)
			panic(err)
		}
	}
	p.finish(f)
}
