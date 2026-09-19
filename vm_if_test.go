package gozero

import (
	"net/http"
	"strings"
	"testing"
)

// TestIfCompileRules pins the named rules that reject input at
// compile time, so the error text stays a contract.
func TestIfCompileRules(t *testing.T) {
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"http.NewRequest": http.NewRequest,
		"yes":             func(s string) bool { return true },
		"record":          func(s string) string { return s },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct {
		src  string
		want string
	}{
		"single exit": {
			`ok := yes(""); if ok { return; }`,
			"single exit",
		},
		"single exit value": {
			`ok := yes(""); if ok { return ok; }`,
			"single exit",
		},
		"flat scope define": {
			`ok := yes(""); if ok { s := record("x"); }`,
			"flat scope",
		},
		"flat scope var": {
			`ok := yes(""); if ok { var s string; }`,
			"flat scope",
		},
		"constant cond": {
			`if true { record("x"); }`,
			"condition form",
		},
		"unbound cond": {
			`if missing { record("x"); }`,
			"condition form",
		},
		"non-bool name": {
			`s := record("x"); if s { record("y"); }`,
			"must be bool",
		},
		"non-bool call": {
			`if record("x") { record("y"); }`,
			"must be bool",
		},
		"non-bool field": {
			"req := http.NewRequest(\"GET\", \"https://h/\")\nif req.Method {\n\trecord(\"x\")\n}\n",
			"must be bool",
		},
		"assign before declare": {
			`if yes("") { s = "x"; }`,
			"not defined",
		},
	} {
		_, err := rt.Compile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to name %q", name, err, tc.want)
		}
	}
}

// TestHeaderCompileRules pins the named rules of the L3 rung: what a
// composed or arithmetic header rejects at compile time, with the
// rule in the error text.
func TestHeaderCompileRules(t *testing.T) {
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"yes":    func(s string) bool { return true },
		"record": func(s string) string { return s },
		"n64":    func(v int64) int64 { return v },
		"f32":    func(v float64) float32 { return float32(v) },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.BindValue("lim.Zero", int64(0)); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		src  string
		want string
	}{
		"arith cond": {
			`n := n64(1); if n + 1 { record("x"); }`,
			"condition form",
		},
		"constant arith cond": {
			`if 1 + 1 { record("x"); }`,
			"condition form",
		},
		"and on number": {
			`ok := yes(""); n := n64(1); if ok && n { record("x"); }`,
			"must be bool",
		},
		"and on literal": {
			`ok := yes(""); if ok && 5 { record("x"); }`,
			"condition form",
		},
		"not on number": {
			`n := n64(1); if !n { record("x"); }`,
			"must be bool",
		},
		"bool arithmetic": {
			`ok := yes(""); if ok + 1 > 0 { record("x"); }`,
			"numeric arithmetic",
		},
		"string plus": {
			`s := record("x"); if s + "y" == "xy" { record("z"); }`,
			"numeric arithmetic",
		},
		"constant string plus": {
			`s := record("x"); if s == "x" + "y" { record("z"); }`,
			"numeric arithmetic",
		},
		"float modulo": {
			`f := f32(1.5); if f % 1 > 0 { record("x"); }`,
			"numeric arithmetic",
		},
		"division by literal zero": {
			`n := n64(1); if n / 0 == 0 { record("x"); }`,
			"constant division by zero",
		},
		"modulo by literal zero": {
			`n := n64(1); if n % 0 == 0 { record("x"); }`,
			"constant division by zero",
		},
		"division by folded zero": {
			`n := n64(1); if 1 / (2 - 2) < n { record("x"); }`,
			"constant division by zero",
		},
		"float division by zero": {
			`f := f32(1.0); if f / 0.0 > 1.0 { record("x"); }`,
			"constant division by zero",
		},
		"division by zero value binding": {
			`n := n64(1); if n / lim.Zero == 0 { record("x"); }`,
			"constant division by zero",
		},
		"constant comparison folds": {
			`if 1 + 1 == 2 { record("x"); }`,
			"constant comparison",
		},
		"comparison as operand": {
			`a := n64(1); b := n64(2); if (a > b) == true { record("x"); }`,
			"comparison operand",
		},
		"arithmetic on comparison": {
			`a := n64(1); if (a > 1) + 1 > 0 { record("x"); }`,
			"comparison operand",
		},
		"mixed arith types": {
			`n := n64(1); f := f32(1.0); if n + f > 0 { record("x"); }`,
			"identical types",
		},
		"float literal into int arith": {
			`n := n64(1); if n + 1.5 > 0 { record("x"); }`,
			"identical types",
		},
	} {
		_, err := rt.Compile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to name %q", name, err, tc.want)
		}
	}
}

// TestCmpCompileRules pins the named rules of the comparison rung:
// what the header rejects at compile time, with the rule in the
// error text.
func TestCmpCompileRules(t *testing.T) {
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"http.NewRequest": http.NewRequest,
		"yes":             func(s string) bool { return true },
		"record":          func(s string) string { return s },
		"n8":              func(v int64) int8 { return int8(v) },
		"u8":              func(v int64) uint8 { return uint8(v) },
		"n64":             func(v int64) int64 { return v },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct {
		src  string
		want string
	}{
		"constant comparison": {
			`if 1 == 2 { record("x"); }`,
			"constant comparison",
		},
		"identical types": {
			`a := n8(1); b := u8(1); if a == b { record("x"); }`,
			"identical types",
		},
		"string literal against int": {
			`n := n64(1); if n == "x" { record("x"); }`,
			"identical types",
		},
		"float literal against int": {
			`n := n64(1); if n == 1.5 { record("x"); }`,
			"identical types",
		},
		"pointer kind": {
			"a := http.NewRequest(\"GET\", \"https://h/\")\nb := http.NewRequest(\"GET\", \"https://h/\")\nif a == b {\n\trecord(\"x\")\n}\n",
			"comparable scalar",
		},
		"nil operand": {
			`req := http.NewRequest("GET", "https://h/"); if req == nil { record("x"); }`,
			"comparable scalar",
		},
		"bool does not order": {
			`ok := yes(""); if ok < true { record("x"); }`,
			"comparable scalar",
		},
		"unbound operand": {
			`if missing == 1 { record("x"); }`,
			"comparison operand",
		},
	} {
		_, err := rt.Compile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to name %q", name, err, tc.want)
		}
	}
}
