package gozero

import (
	"reflect"
	"strings"
	"testing"
)

// TestVarArgResolution covers the resolver directly: the longest
// dotted prefix wins, field selectors continue from there, and
// addressability follows Go's rule rather than the binding's shape.
func TestVarArgResolution(t *testing.T) {
	type inner struct{ Name string }
	type outer struct {
		Label string
		In    inner
		P     *inner
	}

	val := outer{Label: "l", In: inner{Name: "n"}, P: &inner{Name: "p"}}
	c := &Compiler{vars: map[string]varBinding{}, types: map[string]reflect.Type{}}
	c.vars["cfg"] = varBinding{val: reflect.ValueOf(val)}
	c.vars["cfg.deep"] = varBinding{val: reflect.ValueOf(val)}
	mutable := val
	c.vars["mut"] = varBinding{val: reflect.ValueOf(&mutable).Elem(), mutable: true}

	// The longest prefix wins, so cfg.deep is one name and not a field
	// of cfg.
	if _, rest, ok := c.varOf([]string{"cfg", "deep", "Label"}); !ok || len(rest) != 1 || rest[0] != "Label" {
		t.Errorf("varOf picked the wrong prefix: rest=%v ok=%v", rest, ok)
	}

	// A field of a value binding reads through vaField.
	a, ft, ok, err := c.varArg([]string{"cfg", "Label"}, false)
	if err != nil || !ok {
		t.Fatalf("cfg.Label: ok=%v err=%v", ok, err)
	}
	if a.kind != vaField || ft.Kind() != reflect.String {
		t.Errorf("cfg.Label compiled to kind %d of type %s", a.kind, ft)
	}

	// An unknown field is an error, not a miss: the name did resolve.
	if _, _, ok, err := c.varArg([]string{"cfg", "Nope"}, false); !ok || err == nil {
		t.Errorf("cfg.Nope: ok=%v err=%v, want a resolved name with an error", ok, err)
	}

	// A path naming nothing is a miss, which leaves it to the other
	// resolvers rather than failing the compile here.
	if _, _, ok, err := c.varArg([]string{"absent"}, false); ok || err != nil {
		t.Errorf("absent: ok=%v err=%v, want a clean miss", ok, err)
	}

	// Both kinds are addressable; they differ in what they address,
	// which materializeVarCells decides rather than varArg.
	ia, it, _, err := c.varArg([]string{"cfg"}, true)
	if err != nil {
		t.Fatalf("&cfg: %v", err)
	}
	if !ia.addrOf || it.Kind() != reflect.Pointer || it.Elem() != reflect.TypeOf(val) {
		t.Errorf("&cfg compiled to addrOf=%v type=%s", ia.addrOf, it)
	}
	if _, _, _, err := c.varArg([]string{"cfg", "In", "Name"}, true); err != nil {
		t.Errorf("&cfg.In.Name: %v", err)
	}
	if _, _, _, err := c.varArg([]string{"cfg", "P", "Name"}, true); err != nil {
		t.Errorf("&cfg.P.Name: %v", err)
	}
	// And a mutable root is addressable throughout.
	pa, pt, _, err := c.varArg([]string{"mut"}, true)
	if err != nil {
		t.Fatalf("&mut: %v", err)
	}
	if !pa.addrOf || pt.Kind() != reflect.Pointer {
		t.Errorf("&mut compiled to addrOf=%v type=%s", pa.addrOf, pt)
	}
}

// TestDerefArg pins the static half of the pointer read: a non-pointer
// operand is refused when the program compiles.
func TestDerefArg(t *testing.T) {
	src := &vmArg{kind: vaConst, iface: -1}
	if _, _, err := derefArg(src, reflect.TypeFor[int](), "n"); err == nil {
		t.Error("dereferencing an int should not compile")
	}
	if _, _, err := derefArg(src, nil, "n"); err == nil {
		t.Error("dereferencing an untyped value should not compile")
	}
	a, et, err := derefArg(src, reflect.TypeFor[*int](), "p")
	if err != nil {
		t.Fatal(err)
	}
	if a.kind != vaDeref || et.Kind() != reflect.Int {
		t.Errorf("*p compiled to kind %d of type %s", a.kind, et)
	}
}

// TestVarSetNilPointer covers the run-time half of "*p = v": a nil
// pointer is an error rather than a crash.
func TestVarSetNilPointer(t *testing.T) {
	var p *int
	vs := &vmVarSet{
		name: "*p",
		via:  &vmArg{kind: vaConst, val: reflect.ValueOf(p), typ: reflect.TypeFor[*int](), iface: -1},
		val:  &vmArg{kind: vaConst, val: reflect.ValueOf(1), typ: reflect.TypeFor[int](), iface: -1},
	}
	err := vs.apply(t.Context(), nil, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "nil pointer") {
		t.Errorf("want a nil pointer error, got %v", err)
	}
}
