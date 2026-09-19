package gozero

import (
	"context"
	"fmt"
	"unsafe" // also required by go:linkname
)

// The step statements on the direct tier. A step is a load, an add
// and a store at the slot's frame offset: the value never leaves its
// class, so nothing allocates and nothing bridges, and a step never
// appears in Supports output. The bits travel at the class's own
// width through loadN and storeN, which is what makes the wraparound
// of every integer width match compiled Go; floats go through loadF
// and storeF the same way.

// incNode compiles n++ and n--.
func (c *jitCompiler) incNode(in *vmInc) (nodeE, error) {
	field, ok := c.slotOf[in.slot]
	if !ok {
		return nil, fmt.Errorf("a stepped name has no slot")
	}
	cl := layoutOf(c.types[field])
	off := c.offs[field]
	if cl.float() {
		d := float64(in.delta)
		return func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) error {
			at := unsafe.Add(fr, off)
			storeF(cl, at, loadF(at, cl)+d)
			return nil
		}, nil
	}
	if cl.scalar() && cl != lBool {
		// The step as two's-complement bits: adding ^uint64(0)
		// subtracts one at every width once storeN truncates.
		d := uint64(in.delta)
		return func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) error {
			at := unsafe.Add(fr, off)
			storeN(cl, at, loadN(at, cl)+d)
			return nil
		}, nil
	}
	// compileInc admits only integer and float kinds, and every one
	// of those has a class, so this is unreachable; it reports rather
	// than returning a nil closure if a kind is ever added above it.
	return nil, fmt.Errorf("a %s cannot be stepped", cl)
}
