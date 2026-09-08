package gozero

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"unsafe" // also required by go:linkname
)

// unsafeNew is the allocator reflect.New itself calls. Going straight
// to it skips reflect.New's pointer-type lookup. The argument is the
// frame struct's rtype, so the block comes back with the right size and
// pointer map and the collector scans the slots correctly.
//
//go:linkname unsafeNew reflect.unsafe_New
func unsafeNew(rtype unsafe.Pointer) unsafe.Pointer

// The step JIT compiles a program into a tree of typed closures.
//
// Calls are made through layout classes, not types. A parameter or
// result is one of four shapes: pointer-shaped (one word), string (two),
// interface (two) or slice (three). A bound func whose classes match a
// shape in the table is reinterpreted as the shape type and called
// directly, exactly as jit.go does for a single statement.
//
// A value flows from the call that produces it to the call that
// consumes it as the return value of a closure, so it lives in a Go
// local and needs no storage of its own. Only a name read by a later
// statement gets a frame slot, and a program with no such name
// allocates nothing beyond what its bindings allocate.
//
// Interface arguments carry a precomputed itab. Both the concrete type
// and the interface type are known when the program compiles, and an
// itab depends on nothing else, so the pair is built once with reflect
// and only the data word varies per call.
//
// A program JITs whole or not at all. One call outside the table sends
// it to the reflect evaluator in vm.go, which stays the general
// mechanism, and the equivalence test in stepjit_test.go pins the two
// against each other.

const (
	lBad   layout = iota
	lPtr          // one word, pointer-shaped
	lStr          // two words
	lIface        // two words
	lSlice        // three words
	lErr          // a trailing error result, two words
	lNone         // a call with no result other than a possible error

	// Scalars. Each width is its own class because the cast that makes
	// a direct call needs the exact Go type: an int32 parameter is four
	// bytes where an int64 is eight, and a float travels in a different
	// register file from an integer of the same width.
	lBool
	lI8
	lI16
	lI32
	lI64
	lU8
	lU16
	lU32
	lU64
	lF32
	lF64
)

// The node types, one per layout class. Each evaluates its subtree and
// returns the value in that class. f is the frame, nil for a program
// that needs no slots.
type (
	nodeP func(f unsafe.Pointer, ctx context.Context, stack map[string]any, dest any) (unsafe.Pointer, error)
	nodeS func(f unsafe.Pointer, ctx context.Context, stack map[string]any, dest any) (string, error)
	nodeI func(f unsafe.Pointer, ctx context.Context, stack map[string]any, dest any) (ifacePair, error)
	nodeL func(f unsafe.Pointer, ctx context.Context, stack map[string]any, dest any) (sliceHdr, error)
	nodeE func(f unsafe.Pointer, ctx context.Context, stack map[string]any, dest any) error

	// nodeN carries every integer and bool class as the bits of its own
	// width, zero-extended. One closure type covers all of them because
	// the exact Go type is only needed where the call is made, and the
	// shape case there casts back. nodeF does the same for floats.
	nodeN func(f unsafe.Pointer, ctx context.Context, stack map[string]any, dest any) (uint64, error)
	nodeF func(f unsafe.Pointer, ctx context.Context, stack map[string]any, dest any) (float64, error)
)

// node is one compiled expression. Exactly one closure is set, named by
// class; lNone marks a call with no result, which only E carries.
type node struct {
	class layout
	P     nodeP
	S     nodeS
	I     nodeI
	L     nodeL
	E     nodeE
	N     nodeN
	F     nodeF
}

// jitProgram is a program whose every call JITs.
type jitProgram struct {
	// bridged lists calls that go through reflect, empty when the whole
	// program is direct calls.
	bridged   []string
	frameType reflect.Type
	frameRT   unsafe.Pointer // nil when the program needs no slots
	stmts     []nodeE

	// retType and retOff describe the slot a trailing "return expr;"
	// leaves its value in. retType is nil when the program has no
	// value, which is the common case: the output goes through dest.
	retType reflect.Type
	retOff  uintptr
}

func (p *jitProgram) run(ctx context.Context, stack map[string]any, dest any) (any, error) {
	var f unsafe.Pointer
	if p.frameRT != nil {
		f = unsafeNew(p.frameRT)
	}
	for _, stmt := range p.stmts {
		if err := stmt(f, ctx, stack, dest); err != nil {
			return nil, err
		}
	}
	if p.retType == nil {
		return nil, nil
	}
	return reflect.NewAt(p.retType, unsafe.Add(f, p.retOff)).Elem().Interface(), nil
}

// asError turns the raw words of an error result into an error.
func asError(e ifacePair) error {
	if e.tab == nil {
		return nil
	}
	return *(*error)(unsafe.Pointer(&e))
}

// jitCompiler holds the state of one program's compilation.
type jitCompiler struct {
	slotOf map[int]int // vmProgram slot -> frame field index
	types  []reflect.Type
	offs   []uintptr
	// bridged names the calls that compile to a reflect bridge rather
	// than a direct call. The program still runs on this tier; Supports
	// reports them so a benchmark knows which statements pay reflect.
	bridged []string
	// writes counts the assignments to each slot. A slot written more
	// than once cannot back an interface argument, because the argument
	// aliases the slot and a later assignment would change the value
	// behind an interface the callee may still hold.
	writes map[int]int
	// splices maps an argument that reads a name to the call that
	// produced it, where planInline decided the value can travel as a
	// return value instead of through a slot. It is a side table rather
	// than an edit of the call tree, because the same tree is what the
	// reflect evaluator runs when the JIT declines: rewriting it would
	// leave the producer both spliced in and still a statement, and the
	// fallback would run it twice.
	splices map[*vmArg]*vmCall
	// stackFields maps a stack name read more than once to the hidden
	// frame field its value is loaded into when the program starts.
	// Each read is then an offset from the frame pointer instead of a
	// map lookup, which means the name is read once per execution: a
	// binding that mutates the stack mid-run is not seen by later uses.
	stackFields map[string]int
}

// jitCompileProgram builds the all-JIT form of a compiled program, or
// returns the reason it cannot. The error is what Runtime.Supports
// reports, so it names the call and the shape that stopped it.
func jitCompileProgram(p *vmProgram) (*jitProgram, error) {
	if p.polymorphic {
		return nil, fmt.Errorf("a name is reassigned at a different type")
	}

	plan, err := planInline(p)
	if err != nil {
		return nil, err
	}

	c := &jitCompiler{slotOf: map[int]int{}, writes: plan.writes, splices: plan.splices, stackFields: map[string]int{}}
	for slot := 0; slot < p.nslots; slot++ {
		if !plan.live[slot] {
			continue
		}
		t := p.slotTypes[slot]
		if t == nil {
			return nil, fmt.Errorf("a name has no type")
		}
		// A type with no layout class, a struct held by value, still
		// gets a frame field: the frame is typed storage and holds
		// anything. Only the uses that must carry the value between
		// calls need a class, and those fail per use, where the call
		// bridges. A field read is an offset, not a transport.
		c.slotOf[slot] = len(c.types)
		c.types = append(c.types, t)
	}

	// A stack name read more than once gets a hidden any field, filled
	// by a loader statement before the program runs; sorting keeps the
	// frame layout deterministic across compilations.
	var hoisted []string
	for name, n := range c.countStackReads(plan) {
		if n > 1 {
			hoisted = append(hoisted, name)
		}
	}
	sort.Strings(hoisted)
	for _, name := range hoisted {
		c.stackFields[name] = len(c.types)
		c.types = append(c.types, reflect.TypeFor[any]())
	}

	jp := &jitProgram{}
	if len(c.types) > 0 {
		fields := make([]reflect.StructField, len(c.types))
		for i, t := range c.types {
			fields[i] = reflect.StructField{Name: fmt.Sprintf("F%d", i), Type: t}
		}
		jp.frameType = reflect.StructOf(fields)
		jp.frameRT = rtypePtr(jp.frameType)
		c.offs = make([]uintptr, len(c.types))
		for i := range c.types {
			c.offs[i] = jp.frameType.Field(i).Offset
		}
	}

	for _, name := range hoisted {
		name, off := name, c.offs[c.stackFields[name]]
		jp.stmts = append(jp.stmts, func(fr unsafe.Pointer, _ context.Context, st map[string]any, _ any) error {
			if v, ok := st[name]; ok {
				*(*any)(unsafe.Add(fr, off)) = v
			}
			return nil
		})
	}

	for _, s := range plan.stmts {
		stmt, err := c.stmtNode(s, jp)
		if err != nil {
			return nil, err
		}
		jp.stmts = append(jp.stmts, stmt)
	}
	if plan.retSlot >= 0 {
		field, ok := c.slotOf[plan.retSlot]
		if !ok {
			return nil, fmt.Errorf("a returned name has no slot")
		}
		jp.retType, jp.retOff = c.types[field], c.offs[field]
	}
	jp.bridged = c.bridged
	return jp, nil
}
