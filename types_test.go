package gozero

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
)

func typeRuntime(t *testing.T) (*Runtime, *any) {
	t.Helper()
	rt := NewRuntime()
	var seen any
	for name, fns := range map[string]map[string]any{
		"http": {"NewRequest": http.NewRequest},
		"json": {"NewEncoder": json.NewEncoder},
		"url":  {"Parse": url.Parse},
	} {
		if err := rt.BindScope(name, fns); err != nil {
			t.Fatal(err)
		}
	}
	for name, fn := range map[string]any{
		"takesInt":  func(n int) (*url.URL, error) { seen = n; return &url.URL{}, nil },
		"takesI8":   func(n int8) (*url.URL, error) { seen = n; return &url.URL{}, nil },
		"takesU32":  func(n uint32) (*url.URL, error) { seen = n; return &url.URL{}, nil },
		"takesF32":  func(n float32) (*url.URL, error) { seen = n; return &url.URL{}, nil },
		"takesAny":  func(v any) (*url.URL, error) { seen = v; return &url.URL{}, nil },
		"takesI64":  func(n int64) (*url.URL, error) { seen = n; return &url.URL{}, nil },
		"takesBool": func(b bool) (*url.URL, error) { seen = b; return &url.URL{}, nil },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	return rt, &seen
}

// run compiles and executes src, returning what dest received.
func runProgram(t *testing.T, rt *Runtime, src string) (string, error) {
	t.Helper()
	fn, err := rt.Compile(src)
	if err != nil {
		return "", err
	}
	var dest bytes.Buffer
	if err := fn.Scan(&dest, nil); err != nil {
		return "", err
	}
	return dest.String(), nil
}

// TestVarDeclaration checks that var puts the zero value of a named
// type in scope and fixes the type of what is assigned to it.
func TestVarDeclaration(t *testing.T) {
	rt, seen := typeRuntime(t)
	for _, tc := range []struct{ name, src, want string }{
		{"assign then read", `var x int64; x = 123; json.NewEncoder(dest).Encode(x);`, "123\n"},
		{"narrower type", `var x int32; x = 7; json.NewEncoder(dest).Encode(x);`, "7\n"},
		{"zero value", `var x int64; json.NewEncoder(dest).Encode(x);`, "0\n"},
		{"string", `var s string; s = "hi"; json.NewEncoder(dest).Encode(s);`, "\"hi\"\n"},
		{"discovered struct", `var u url.URL; json.NewEncoder(dest).Encode(u.Path);`, "\"\"\n"},
	} {
		got, err := runProgram(t, rt, tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: dest = %q, want %q", tc.name, got, tc.want)
		}
	}

	// The declared type reaches the binding.
	*seen = nil
	if _, err := runProgram(t, rt, `var x int64; x = 123; takesI64(x); json.NewEncoder(dest).Encode("ok");`); err != nil {
		t.Fatal(err)
	}
	if *seen != int64(123) {
		t.Errorf("binding saw %v (%T), want int64(123)", *seen, *seen)
	}
}

// TestLiteralTypeInference checks the rule for a name no var statement
// declared: the first binding that takes it decides, and failing that
// the literal keeps the width the parser gave it.
func TestLiteralTypeInference(t *testing.T) {
	rt, seen := typeRuntime(t)
	for _, tc := range []struct {
		name, src string
		want      any
	}{
		{"from the use", `x = 5; takesInt(x);`, int(5)},
		{"from a later use", `x = 5; json.NewEncoder(dest).Encode("a"); takesI8(x);`, int8(5)},
		{"from a nested use", `x = 5; json.NewEncoder(dest).Encode(takesU32(x));`, uint32(5)},
		{"no use, whole number", `x = 5; takesAny(x);`, int64(5)},
		{"no use, decimal", `x = 5.5; takesAny(x);`, float64(5.5)},
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
		{"int32", `x = int32(7); takesAny(x);`, int32(7)},
		{"uint8", `x = uint8(255); takesAny(x);`, uint8(255)},
		{"float32", `x = float32(1.5); takesAny(x);`, float32(1.5)},
		{"negative", `x = int8(-5); takesAny(x);`, int8(-5)},
		{"string", `s = string("hi"); takesAny(s);`, "hi"},
		{"define form", `x := int32(3); takesAny(x);`, int32(3)},
		{"sticky on reassign", `x = int32(5); x = 9; takesAny(x);`, int32(9)},
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
		{"overflow", `x = int8(300);`},
		{"negative unsigned", `x = uint32(-1);`},
		{"decimal point", `x = int32(1.5);`},
		{"declared type wins", `var x int64; x = int32(5);`},
		{"hinted type not overridden by use", `x = int32(5); takesInt(x);`},
		{"a name is not a literal", `y = 5; x = int32(y);`},
		{"unknown type stays an unknown binding", `x = nope(5);`},
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

// TestTypeRegistry checks what a var statement can name. Almost
// everything comes from walking the bindings; BindType covers the rest.
func TestRuntime_BindType(t *testing.T) {
	rt, _ := typeRuntime(t)
	for _, want := range []string{
		"int", "int64", "uint32", "float32", "string", "bool", "any", "error", "[]uint8",
		"*http.Request", "http.Request", "http.Header", "io.Reader", "io.Writer",
		"*json.Encoder", "*url.URL", "url.URL",
	} {
		if _, ok := rt.compiler.lookupType(want); !ok {
			t.Errorf("%s is not in the registry", want)
		}
	}

	if _, err := rt.Compile(`var c io.Closer; json.NewEncoder(dest).Encode("x");`); err == nil {
		t.Fatal("io.Closer should not be reachable from these bindings")
	}
	if err := rt.BindType("io.Closer", (*io.Closer)(nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Compile(`var c io.Closer; json.NewEncoder(dest).Encode("x");`); err != nil {
		t.Errorf("after BindType: %v", err)
	}
}

// TestRuntime_Types checks that the listing carries both the
// predeclared names and what discovery found.
func TestRuntime_Types(t *testing.T) {
	rt, _ := typeRuntime(t)
	names := map[string]bool{}
	for _, name := range rt.Types() {
		names[name] = true
	}
	for _, want := range []string{"string", "*http.Request"} {
		if !names[want] {
			t.Errorf("%s is not listed by Types", want)
		}
	}
}

// TestVarPointerAndSliceTypes checks the prefixes a type name can carry.
func TestVarPointerAndSliceTypes(t *testing.T) {
	rt, _ := typeRuntime(t)
	for _, src := range []string{
		`var r *http.Request; json.NewEncoder(dest).Encode("x");`,
		`var b []uint8; json.NewEncoder(dest).Encode("x");`,
	} {
		if _, err := rt.Compile(src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	if _, err := rt.Compile(`var x nope.Thing; json.NewEncoder(dest).Encode("x");`); err == nil {
		t.Fatal("expected an unknown type to be reported")
	}
}

// TestScalarsJIT checks that the scalar work reaches the direct-call
// tier rather than sending the program to the reflect evaluator: a var
// declaration, a literal assignment, a scalar read back out of a slot,
// a scalar argument at several widths, and a scalar boxed into an
// interface.
func TestScalarsJIT(t *testing.T) {
	rt, seen := typeRuntime(t)
	for _, tc := range []struct {
		name, src, want string
		// wantSeen is the value the binding must receive on the direct
		// tier, for the cases that pass one; nil skips the check.
		wantSeen any
	}{
		{"var and assign", `var x int64; x = 1; json.NewEncoder(dest).Encode(x);`, "1\n", nil},
		{"inferred literal", `x = 1; json.NewEncoder(dest).Encode(x);`, "1\n", nil},
		{"declared int32", `var x int32; x = 7; json.NewEncoder(dest).Encode(x);`, "7\n", nil},
		{"conversion hint", `x = int32(7); json.NewEncoder(dest).Encode(x);`, "7\n", nil},
		{"declared float64", `var x float64; x = 2.5; json.NewEncoder(dest).Encode(x);`, "2.5\n", nil},
		{"declared bool zero", `var b bool; json.NewEncoder(dest).Encode(b);`, "false\n", nil},
		{"scalar argument", `var x int64; x = 3; takesI64(x); json.NewEncoder(dest).Encode(x);`, "3\n", int64(3)},
		{"literal argument", `json.NewEncoder(dest).Encode(takesInt(42));`, "", int(42)},
		{"scalar field", `req := http.NewRequest("GET", "/"); json.NewEncoder(dest).Encode(req.ContentLength);`, "0\n", nil},
	} {
		if err := rt.Supports(tc.src); err != nil {
			t.Errorf("%s: did not JIT: %v", tc.name, err)
			continue
		}
		*seen = nil
		got, err := runProgram(t, rt, tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if tc.want != "" && got != tc.want {
			t.Errorf("%s: dest = %q, want %q", tc.name, got, tc.want)
		}
		if tc.wantSeen != nil && *seen != tc.wantSeen {
			t.Errorf("%s: binding saw %v (%T), want %v (%T)", tc.name, *seen, *seen, tc.wantSeen, tc.wantSeen)
		}
	}
}
