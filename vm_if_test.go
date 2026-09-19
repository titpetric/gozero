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
