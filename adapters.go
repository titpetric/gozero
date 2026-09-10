package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// Host-interface adapters. reflect cannot mint a named type with
// methods, so a script type satisfies a Go interface through one
// struct the host declares per interface: a func field per method,
// forwarding methods over those fields. BindAdapter validates the
// pair once; at a call site the compiler fills the fields with the
// script's methods and passes the adapter.

// adapterSpec is one registered interface adapter.
type adapterSpec struct {
	iface  reflect.Type
	impl   reflect.Type // the adapter struct, A in *A
	fields []adapterField
}

// adapterField pairs one interface method with the func field that
// carries it.
type adapterField struct {
	method string
	index  []int
	sig    reflect.Type // the receiverless signature
}

// findAdapter reports the registered adapter for an interface.
func (c *Compiler) findAdapter(pt reflect.Type) *adapterSpec {
	return c.adapters[pt]
}

// adaptScript converts a script-typed value into a registered
// adapter for the interface parameter pt, when the script method set
// covers it. ok is false when this is not an adapter situation at
// all; an error means it is one and it cannot compile.
func (c *Compiler) adaptScript(sc *cscope, node *vmArg, st, pt reflect.Type) (*vmArg, bool, error) {
	if pt.Kind() != reflect.Interface || pt.NumMethod() == 0 || sc.script == nil {
		return nil, false, nil
	}
	base := st
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	if sc.script.names[base] == "" {
		return nil, false, nil
	}
	spec := c.findAdapter(pt)
	if spec == nil {
		return nil, true, fmt.Errorf("compile: cannot use script type %s as %s: no adapter registered, call BindAdapter", sc.typeName(base), sc.typeName(pt))
	}
	out := &vmArg{kind: vaAdapter, x: node, spec: spec, typ: pt, iface: -1}
	for _, af := range spec.fields {
		fn := scriptMethodOf(sc, st, af.method)
		if fn == nil {
			return nil, true, fmt.Errorf("compile: %s does not implement %s: missing method %s", sc.typeName(base), sc.typeName(pt), af.method)
		}
		if fn.recvT.Kind() == reflect.Pointer && st.Kind() != reflect.Pointer {
			return nil, true, fmt.Errorf("compile: %s does not implement %s: method %s has a pointer receiver", sc.typeName(base), sc.typeName(pt), af.method)
		}
		if recvlessSig(fn.sig) != af.sig {
			return nil, true, fmt.Errorf("compile: %s.%s is %s, want %s", sc.typeName(base), af.method, recvlessSig(fn.sig), af.sig)
		}
		out.adapterFns = append(out.adapterFns, fn)
	}
	return out, true, nil
}

// recvlessSig strips a method signature's receiver parameter.
func recvlessSig(sig reflect.Type) reflect.Type {
	in := make([]reflect.Type, 0, sig.NumIn()-1)
	for i := 1; i < sig.NumIn(); i++ {
		in = append(in, sig.In(i))
	}
	out := make([]reflect.Type, sig.NumOut())
	for i := range out {
		out[i] = sig.Out(i)
	}
	return reflect.FuncOf(in, out, sig.IsVariadic())
}

// buildAdapter is the run-time half: a fresh adapter struct whose
// func fields trampoline into the script methods over the receiver
// value the conversion site evaluated. The adapter escapes to the
// callee by contract and is never pooled.
func buildAdapter(ctx context.Context, spec *adapterSpec, fns []*scriptFn, recv reflect.Value) reflect.Value {
	pv := reflect.New(spec.impl)
	sv := pv.Elem()
	for i, af := range spec.fields {
		fn := fns[i]
		mrecv := recv
		if fn.recvT.Kind() != reflect.Pointer && mrecv.Kind() == reflect.Pointer {
			mrecv = mrecv.Elem()
		}
		sv.FieldByIndex(af.index).Set(fn.materializeMethod(ctx, af.sig, mrecv))
	}
	return pv
}

// validateAdapter checks a proto against an interface at bind time:
// *A implements I, and each interface method M has an exported
// settable func field named M plus Func with the receiverless
// signature.
func validateAdapter(iface, proto reflect.Type) (*adapterSpec, error) {
	if iface.Kind() != reflect.Interface || iface.NumMethod() == 0 {
		return nil, fmt.Errorf("bind: %s is not a non-empty interface", iface)
	}
	if proto == nil || proto.Kind() != reflect.Pointer || proto.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("bind: an adapter registers as a nil pointer to its struct, (*HandlerAdapter)(nil)")
	}
	if !proto.Implements(iface) {
		return nil, fmt.Errorf("bind: %s does not implement %s", proto, iface)
	}
	impl := proto.Elem()
	spec := &adapterSpec{iface: iface, impl: impl}
	for i := 0; i < iface.NumMethod(); i++ {
		m := iface.Method(i)
		f, ok := impl.FieldByName(m.Name + "Func")
		if !ok || f.PkgPath != "" {
			return nil, fmt.Errorf("bind: adapter %s is missing field %sFunc for %s.%s", proto, m.Name, iface, m.Name)
		}
		if f.Type != m.Type {
			return nil, fmt.Errorf("bind: adapter field %sFunc is %s, want %s", m.Name, f.Type, m.Type)
		}
		spec.fields = append(spec.fields, adapterField{method: m.Name, index: f.Index, sig: m.Type})
	}
	return spec, nil
}
