package gozero

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// fieldPair and fieldInner are host types with a struct held by
// value, the shape the value-chain case of fieldAddr compiles.
type fieldInner struct {
	N int64
	S string
}

type fieldPair struct {
	Inner fieldInner
	M     int64
}

// TestFieldChainBothTiers reads through a struct held by value inside
// another struct on both tiers and requires identical output: the
// offsets of value steps add onto the frame slot's address.
func TestFieldChainBothTiers(t *testing.T) {
	rt := pairRuntime(t)
	if err := rt.BindType("fieldPair", fieldPair{}); err != nil {
		t.Fatal(err)
	}
	const src = `
		var p fieldPair;
		p.M = 9;
		json.NewEncoder(dest).Encode(p.Inner.N);
		json.NewEncoder(dest).Encode(p.Inner.S);
		json.NewEncoder(dest).Encode(p.M);
	`
	jit, slow := compilePair(t, rt, src)
	want := "0\n\"\"\n9\n"
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		var dest bytes.Buffer
		if _, err := fn(context.Background(), nil, &dest); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if dest.String() != want {
			t.Errorf("%s: dest = %q, want %q", name, dest.String(), want)
		}
	}
}

// TestFieldChainNilPointer reads through a nil pointer step on both
// tiers: both must fail rather than fault, naming the nil type.
func TestFieldChainNilPointer(t *testing.T) {
	rt := pairRuntime(t)
	const src = `
		var r http.Request;
		json.NewEncoder(dest).Encode(r.Response.StatusCode);
	`
	jit, slow := compilePair(t, rt, src)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		var dest bytes.Buffer
		_, err := fn(context.Background(), nil, &dest)
		if err == nil || !strings.Contains(err.Error(), "nil *http.Response") {
			t.Errorf("%s: err = %v, want a nil *http.Response read error", name, err)
		}
	}
}

// embBase and embHolder are host types with an embedded pointer, the
// one promotion shape whose offsets do not add: reaching N crosses an
// allocation boundary, so the direct tier declines it by name.
type embBase struct {
	N int64
}

type embHolder struct {
	*embBase
	M int64
}

// TestFieldPromotedPointerDeclines pins the flatField boundary: a
// field promoted through an embedded pointer stays on the reflect
// evaluator with the named reason, and still reads the right value.
func TestFieldPromotedPointerDeclines(t *testing.T) {
	rt := pairRuntime(t)
	if err := rt.Bind("mkHolder", func() *embHolder {
		return &embHolder{embBase: &embBase{N: 7}, M: 2}
	}); err != nil {
		t.Fatal(err)
	}
	const src = `
		h := mkHolder();
		json.NewEncoder(dest).Encode(h.N);
	`
	if err := rt.Supports(src); err == nil || !strings.Contains(err.Error(), "promoted through an embedded *gozero.embBase") {
		t.Fatalf("Supports = %v, want the embedded-pointer reason", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	var dest bytes.Buffer
	if err := fn.Scan(&dest, nil); err != nil {
		t.Fatal(err)
	}
	if dest.String() != "7\n" {
		t.Errorf("dest = %q, want %q", dest.String(), "7\n")
	}
}
