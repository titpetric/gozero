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
// tiers: both must fail rather than fault, naming the nil type. The
// chain bottoms out at a frame slot, then derefs the pointer field
// fieldAddr loaded, so the nil check is the load's own.
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
