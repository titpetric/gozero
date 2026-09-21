package gozero

import (
	"net/http"
	"strings"
	"testing"
)

// TestIfCompileRules pins the named rules that reject input at
// compile time, so the error text stays a contract.
func TestIfCompileRules(t *testing.T) {
	rt := ifCompileRuntime(t)
	for name, tc := range map[string]struct {
		src  string
		want string
	}{
		"flat scope define": {
			`ok := yes(""); if ok { s := record("x"); }`,
			"flat scope",
		},
		"flat scope var": {
			`ok := yes(""); if ok { var s string; }`,
			"flat scope",
		},
		"flat scope nested": {
			`ok := yes(""); if ok { if ok { var s string; } }`,
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
		"no such field": {
			"req := http.NewRequest(\"GET\", \"https://h/\")\nif req.Missing {\n\trecord(\"x\")\n}\n",
			"has no field",
		},
		"valueless call cond": {
			`if nothing() { record("x"); }`,
			"returns no value",
		},
		"assign before declare": {
			`if yes("") { s = "x"; }`,
			"not defined",
		},
		"if is reserved": {
			`if := yes("")`,
			"expected an if condition",
		},
	} {
		_, err := rt.Compile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to name %q", name, err, tc.want)
		}
	}
}

// TestIfCompileAccepts is the other half: the forms the rung admits
// compile, including a return inside an arm, which the arms carry
// through errProgramReturn rather than rejecting as a second exit.
func TestIfCompileAccepts(t *testing.T) {
	rt := ifCompileRuntime(t)
	for name, src := range map[string]string{
		"name cond":       `ok := yes(""); if ok { record("x"); }`,
		"field cond":      "req := http.NewRequest(\"GET\", \"https://h/\")\nif req.Close {\n\trecord(\"x\")\n}\n",
		"call cond":       `if yes("") { record("x"); }`,
		"else if chain":   `a := yes(""); b := no(""); if a { record("a"); } else if b { record("b"); } else { record("c"); }`,
		"assign in arm":   `s := ""; if yes("") { s = "x"; }; record(s);`,
		"step in arm":     `n := 0; if yes("") { n++; }`,
		"return in arm":   `s := ""; if yes("") { return s; }; record(s);`,
		"bare return":     `if yes("") { return; }; record("tail");`,
		"nested return":   `s := ""; if yes("") { if no("") { return s; } }; record(s);`,
		"var before arms": "var s string\nif yes(\"\") {\n\ts = \"x\"\n}\nrecord(s)\n",
	} {
		if _, err := rt.Compile(src); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// ifCompileRuntime is the binding surface the compile-rule tests
// read: a bool source, a string recorder, and a call with no result.
func ifCompileRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"http.NewRequest": http.NewRequest,
		"yes":             func(s string) bool { return true },
		"no":              func(s string) bool { return false },
		"record":          func(s string) string { return s },
		"nothing":         func() {},
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	return rt
}
