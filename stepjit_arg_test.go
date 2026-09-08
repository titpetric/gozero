package gozero

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"
)

// TestStepJITReassignedSlotIsCopied pins the rule behind the aliasing.
// An interface argument taken from a slot normally points at the slot
// rather than a copy, which is what reaches native's allocation count.
// That is only legal while the slot is written once: here it is written
// twice, so the value handed to the callee must be a copy taken at the
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
// write guard: a field of a nil pointer reports rather than faults.
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

// TestHoistedStackNameSnapshot pins the documented semantics of the
// hidden frame field: a stack name read more than once is loaded once
// when the program starts, so a binding that mutates the stack mid-run
// is not seen by later uses of the same name.
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
