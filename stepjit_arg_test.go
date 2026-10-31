package gozero

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// TestStepJITReassignedSlotIsCopied pins the rule behind the aliasing.
// An interface argument taken from a slot normally points at the slot
// and skips a copy. That reaches native's allocation count. Aliasing
// is only legal while the slot takes one write: because the program
// here writes it twice, the callee must receive a copy taken at the
// call, not a window onto whatever the slot holds later.
func TestStepJITReassignedSlotIsCopied(t *testing.T) {
	rt := pairRuntime(t)
	var kept any
	if err := rt.Bind("keep", func(v any) (*url.URL, error) {
		kept = v // the callee retains the interface past the call
		return &url.URL{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	const src = `
		s := url.Parse("/a").String();
		keep(s);
		s = url.Parse("/b").String();
		json.NewEncoder(dest).Encode(s);
	`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("expected this to JIT: %v", err)
	}
	jit, slow := compilePair(t, rt, src)
	for _, tc := range []struct {
		name string
		fn   CompiledFunc
	}{{"jit", jit}, {"reflect", slow}} {
		kept = nil
		var dest bytes.Buffer
		if _, err := tc.fn(context.Background(), nil, &dest); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if kept != "/a" {
			t.Errorf("%s: callee kept %v, want /a: the reassignment leaked through the interface", tc.name, kept)
		}
		if got, want := dest.String(), "\"/b\"\n"; got != want {
			t.Errorf("%s: dest = %q, want %q", tc.name, got, want)
		}
	}
}

// TestFieldReadClasses covers the loads fieldNode compiles beyond
// pointer and string fields: float, slice and interface fields, read
// as arguments.
func TestFieldReadClasses(t *testing.T) {
	rt := boxRuntime(t)
	if err := rt.Bind("fill", func() *fieldBox {
		return &fieldBox{N: 7, F: 1.5, S: "s", Parts: []string{"x", "y"}, V: "iv"}
	}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ src, want string }{
		"float":  {`b := fill(); json.NewEncoder(dest).Encode(b.F)`, "1.5\n"},
		"slice":  {`b := fill(); json.NewEncoder(dest).Encode(b.Parts)`, "[\"x\",\"y\"]\n"},
		"iface":  {`b := fill(); json.NewEncoder(dest).Encode(b.V)`, "\"iv\"\n"},
		"scalar": {`b := fill(); json.NewEncoder(dest).Encode(b.N)`, "7\n"},
	} {
		fn, err := rt.Compile(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var dest bytes.Buffer
		if err := fn.Scan(&dest, nil); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if dest.String() != tc.want {
			t.Errorf("%s: dest = %q, want %q", name, dest.String(), tc.want)
		}
	}
}

// TestFieldReadNilBase pins the run-time read guard, the mirror of the
// write guard: a field of a nil pointer reports an error and does not fault.
func TestFieldReadNilBase(t *testing.T) {
	rt := boxRuntime(t)
	fn, err := rt.Compile(`b := nilBox(); json.NewEncoder(dest).Encode(b.F)`)
	if err != nil {
		t.Fatal(err)
	}
	var dest bytes.Buffer
	err = fn.Scan(&dest, nil)
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("err = %v, want the nil read reported", err)
	}
}

// wantScalar binds a checker that asserts the callee received exactly
// want. A width or sign slip in the carrier shows up as the value the
// callee saw.
func wantScalar[T comparable](t *testing.T, rt *Runtime, name string, want T) {
	t.Helper()
	if err := rt.Bind(name, func(v T) error {
		if v != want {
			return fmt.Errorf("saw %v, want %v", v, want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestStackScalarConvWidths exercises every stackScalarConvs entry on
// the direct tier: one binding per predeclared scalar type, its value
// read off the caller's stack. The negative values pin the carrier
// contract the intBits comment states: the nodeN bits may be sign- or
// zero-extended past the parameter's width, because every consumer
// converts at that width before reading the value.
func TestStackScalarConvWidths(t *testing.T) {
	rt := NewRuntime()
	wantScalar(t, rt, "checkBool", true)
	wantScalar(t, rt, "checkInt", -3)
	wantScalar(t, rt, "checkI8", int8(-8))
	wantScalar(t, rt, "checkI16", int16(-16))
	wantScalar(t, rt, "checkI32", int32(-32))
	wantScalar(t, rt, "checkI64", int64(-64))
	wantScalar(t, rt, "checkUint", uint(3))
	wantScalar(t, rt, "checkU8", uint8(8))
	wantScalar(t, rt, "checkU16", uint16(16))
	wantScalar(t, rt, "checkU32", uint32(32))
	wantScalar(t, rt, "checkU64", uint64(1)<<63)
	wantScalar(t, rt, "checkF32", float32(-0.5))
	wantScalar(t, rt, "checkF64", 2.5)
	for name, v := range map[string]any{
		"checkBool": true,
		"checkInt":  -3,
		"checkI8":   int8(-8),
		"checkI16":  int16(-16),
		"checkI32":  int32(-32),
		"checkI64":  int64(-64),
		"checkUint": uint(3),
		"checkU8":   uint8(8),
		"checkU16":  uint16(16),
		"checkU32":  uint32(32),
		"checkU64":  uint64(1) << 63,
		"checkF32":  float32(-0.5),
		"checkF64":  2.5,
	} {
		src := name + `(v);`
		if err := rt.Supports(src); err != nil {
			t.Errorf("%s: did not JIT: %v", name, err)
			continue
		}
		if _, err := rt.Eval[any](src, map[string]any{"v": v}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestHoistedStackNameSnapshot pins the documented semantics of the
// hidden frame field: the program loads a stack name read more than
// once a single time when it starts. Later uses of the same name miss
// a binding's mid-run mutation of the stack.
func TestHoistedStackNameSnapshot(t *testing.T) {
	rt := boxRuntime(t)
	stack := map[string]any{"v": "before"}
	var got []any
	if err := rt.Bind("poke", func(v any) *fieldBox {
		got = append(got, v)
		stack["v"] = "after"
		return &fieldBox{}
	}); err != nil {
		t.Fatal(err)
	}
	const src = `
		poke(v)
		poke(v)
		json.NewEncoder(dest).Encode("done")
	`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	var dest bytes.Buffer
	if err := fn.Scan(&dest, stack); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "before" || got[1] != "before" {
		t.Errorf("callee saw %v, want the start-of-run snapshot twice", got)
	}
	// The next execution reads the map again and sees the write.
	got = nil
	if err := fn.Scan(&dest, stack); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "after" {
		t.Errorf("second run saw %v, want the mutated value", got)
	}
}
