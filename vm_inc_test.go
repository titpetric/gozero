package gozero

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func incRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime()
	if err := rt.BindScope("fmt", map[string]any{"Sprintf": fmt.Sprintf}); err != nil {
		t.Fatal(err)
	}
	return rt
}

// incPrograms is the step semantics across every scalar class,
// wraparound included, pinned as formatted strings. Both tiers run
// the same table: TestIncDecReflect here, and the equivalence test
// in stepjit_inc_test.go, which requires the JIT to agree.
var incPrograms = map[string]struct{ src, want string }{
	"int64 up and down": {
		`n := 7; n++; n++; n--; s := fmt.Sprintf("%d", n); return s;`,
		"8",
	},
	"int wraps like Go": {
		`var n int; n = 5; n--; s := fmt.Sprintf("%d", n); return s;`,
		"4",
	},
	"int8 overflow wraps": {
		`var n int8; n = 127; n++; s := fmt.Sprintf("%d", n); return s;`,
		"-128",
	},
	"int16 step": {
		`var n int16; n = 300; n++; s := fmt.Sprintf("%d", n); return s;`,
		"301",
	},
	"int32 below zero": {
		`n := int32(0); n--; n--; s := fmt.Sprintf("%d", n); return s;`,
		"-2",
	},
	"uint8 wraps up": {
		`var n uint8; n = 255; n++; s := fmt.Sprintf("%d", n); return s;`,
		"0",
	},
	"uint8 wraps down": {
		`var n uint8; n--; s := fmt.Sprintf("%d", n); return s;`,
		"255",
	},
	"uint16 wraps up": {
		`var n uint16; n = 65535; n++; s := fmt.Sprintf("%d", n); return s;`,
		"0",
	},
	"uint32 wraps down": {
		`var n uint32; n--; s := fmt.Sprintf("%d", n); return s;`,
		"4294967295",
	},
	"uint64 wraps down": {
		`var n uint64; n--; s := fmt.Sprintf("%d", n); return s;`,
		"18446744073709551615",
	},
	"uint step": {
		`var n uint; n = 1; n++; s := fmt.Sprintf("%d", n); return s;`,
		"2",
	},
	"uintptr step": {
		`var n uintptr; n++; s := fmt.Sprintf("%d", n); return s;`,
		"1",
	},
	"float32 step": {
		`n := float32(1.5); n++; s := fmt.Sprintf("%v", n); return s;`,
		"2.5",
	},
	"float64 down": {
		`n := 2.5; n--; n--; s := fmt.Sprintf("%v", n); return s;`,
		"0.5",
	},
	"zero value from var": {
		`var n int64; n++; s := fmt.Sprintf("%d", n); return s;`,
		"1",
	},
}

// TestIncDecReflect pins the step semantics on the reflect tier, the
// semantic reference: every scalar class steps at its own width and
// wraps the way compiled Go wraps.
func TestIncDecReflect(t *testing.T) {
	rt := incRuntime(t)
	for name, tc := range incPrograms {
		prog, err := (&Parser{}).Parse(tc.src)
		if err != nil {
			t.Errorf("%s: parse: %v", name, err)
			continue
		}
		p, err := rt.compiler.compileProgram(prog)
		if err != nil {
			t.Errorf("%s: compile: %v", name, err)
			continue
		}
		got, err := p.run(context.Background(), nil, nil)
		if err != nil {
			t.Errorf("%s: run: %v", name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %s", name, got, tc.want)
		}
	}
}

// TestIncDecSharedLiteral runs one compiled program twice. The value
// a literal assignment stores is prebuilt and shared between runs;
// a step that mutated it in place would make the second run start
// from the first run's result.
func TestIncDecSharedLiteral(t *testing.T) {
	rt := incRuntime(t)
	prog, err := (&Parser{}).Parse(`n := 5; n++; s := fmt.Sprintf("%d", n); return s;`)
	if err != nil {
		t.Fatal(err)
	}
	p, err := rt.compiler.compileProgram(prog)
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 2; run++ {
		got, err := p.run(context.Background(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != "6" {
			t.Fatalf("run %d: got %v, want 6", run, got)
		}
	}
}

// TestIncDecCompileErrors names the rules: a step needs a defined
// name of an integer or float type.
func TestIncDecCompileErrors(t *testing.T) {
	rt := incRuntime(t)
	for name, tc := range map[string]struct{ src, want string }{
		"undefined":   {`n++;`, "n is not defined"},
		"string":      {`s := "a"; s++;`, "s is string, ++ and -- step an integer or float"},
		"bool":        {`b := true; b--;`, "b is bool, ++ and -- step an integer or float"},
		"stack value": {`tb++;`, "tb is not defined"},
	} {
		_, err := rt.Compile(tc.src)
		if err == nil {
			t.Errorf("%s: expected a compile error for %q", name, tc.src)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not name the rule %q", name, err, tc.want)
		}
	}
}
