package gozero

import (
	"testing"
)

// TestLiteralTypeInference checks the rule for a name no var statement
// declared: the first binding that takes it decides, and failing that
// the literal keeps the width the parser gave it.
func TestLiteralTypeInference(t *testing.T) {
	rt, seen := typeRuntime(t)
	for _, tc := range []struct {
		name, src string
		want      any
	}{
		{"from the use", `x := 5; takesInt(x);`, int(5)},
		{"from a later use", `x := 5; json.NewEncoder(dest).Encode("a"); takesI8(x);`, int8(5)},
		{"from a nested use", `x := 5; json.NewEncoder(dest).Encode(takesU32(x));`, uint32(5)},
		{"no use, whole number", `x := 5; takesAny(x);`, int64(5)},
		{"no use, decimal", `x := 5.5; takesAny(x);`, float64(5.5)},
	} {
		*seen = nil
		if _, err := runProgram(t, rt, tc.src); err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if *seen != tc.want {
			t.Errorf("%s: binding saw %v (%T), want %v (%T)", tc.name, *seen, *seen, tc.want, tc.want)
		}
	}
}

// TestConversionHint checks the x = int32(0) form: a call whose path
// names a registered type rather than a binding fixes the literal's
// type at the assignment, the way a var declaration would.
func TestConversionHint(t *testing.T) {
	rt, seen := typeRuntime(t)
	for _, tc := range []struct {
		name, src string
		want      any
	}{
		{"int32", `x := int32(7); takesAny(x);`, int32(7)},
		{"uint8", `x := uint8(255); takesAny(x);`, uint8(255)},
		{"float32", `x := float32(1.5); takesAny(x);`, float32(1.5)},
		{"negative", `x := int8(-5); takesAny(x);`, int8(-5)},
		{"string", `s := string("hi"); takesAny(s);`, "hi"},
		{"define form", `x := int32(3); takesAny(x);`, int32(3)},
		{"sticky on reassign", `x := int32(5); x = 9; takesAny(x);`, int32(9)},
		{"matches a var", `var x int32; x = int32(4); takesAny(x);`, int32(4)},
	} {
		*seen = nil
		if _, err := runProgram(t, rt, tc.src); err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if *seen != tc.want {
			t.Errorf("%s: binding saw %v (%T), want %v (%T)", tc.name, *seen, *seen, tc.want, tc.want)
		}
	}
}

// TestConversionHintErrors checks that a hint reports the same
// mismatches a var declaration does, and that a hint is only for
// literals.
func TestConversionHintErrors(t *testing.T) {
	rt, _ := typeRuntime(t)
	for _, tc := range []struct{ name, src string }{
		{"overflow", `x := int8(300);`},
		{"negative unsigned", `x := uint32(-1);`},
		{"decimal point", `x := int32(1.5);`},
		{"declared type wins", `var x int64; x = int32(5);`},
		{"hinted type not overridden by use", `x := int32(5); takesInt(x);`},
		{"a name is not a literal", `y := 5; x := int32(y);`},
		{"unknown type stays an unknown binding", `x := nope(5);`},
	} {
		_, err := rt.Compile(tc.src)
		if err == nil {
			t.Errorf("%s: expected a compile error", tc.name)
			continue
		}
		t.Logf("%s: %v", tc.name, err)
	}
}

// TestVarBeatsInference checks that a declared type is not overridden
// by how the name is used: the mismatch is reported.
func TestVarBeatsInference(t *testing.T) {
	rt, _ := typeRuntime(t)
	_, err := rt.Compile(`var x int64; x = 5; takesInt(x);`)
	if err == nil {
		t.Fatal("expected int64 not to satisfy an int parameter")
	}
	t.Log(err)
	if _, err := rt.Compile(`var x int64; x = "s";`); err == nil {
		t.Fatal("expected a string not to fit an int64")
	}
}
