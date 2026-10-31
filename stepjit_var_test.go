package gozero

import (
	"io"
	"strings"
	"testing"
	"time"
)

// TestVarNodeTiers pins which value reads the direct tier takes. A
// read is one load from a fixed address. A write falls outside the
// table, and the whole program then goes to the reflect evaluator,
// which keeps the statement.
func TestVarNodeTiers(t *testing.T) {
	rt := NewRuntime()
	count := 0
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for name, v := range map[string]any{
		"label":    "hello",
		"dur":      time.Hour,
		"sentinel": io.EOF,
		"count":    &count,
		// Because these shapes are in the table, a tier report
		// turns on the argument alone.
		"pair":    func(a, b string) error { return nil },
		"countIn": func(p *int, s string) int64 { return int64(*p + len(s)) },
		"takeAny": func(p *int, v any) error { return nil },
	} {
		must(rt.Bind(name, v))
	}

	for _, src := range []string{
		`pair(label, "x")`,
		`n := countIn(count, "x")`,
		`takeAny(count, dur)`,
		`takeAny(count, sentinel)`,
		`takeAny(count, label)`,
	} {
		if err := rt.Supports(src); err != nil {
			t.Errorf("%q should be direct: %v", src, err)
		}
	}
	// A write has no node. The refusal names the table it falls
	// outside, which makes the fallback visible.
	err := rt.Supports(`label = "other"`)
	if err == nil || !strings.Contains(err.Error(), "not in the table") {
		t.Errorf("a write should report its fallback, got %v", err)
	}
}

// TestVarNodeReadsTheBoundValue pins the fixed-address load: the node
// reads the value Bind registered. A rebind does not reach a program
// already compiled.
func TestVarNodeReadsTheBoundValue(t *testing.T) {
	rt := NewRuntime()
	seen := ""
	if err := rt.Bind("label", "before"); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("pair", func(a, b string) error { seen = a; return nil }); err != nil {
		t.Fatal(err)
	}
	src := `pair(label, "x")`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("expected the direct tier: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	if seen != "before" {
		t.Fatalf("read %q", seen)
	}
}

// TestVarCellSeedsTheFrame covers the direct tier's prologue for an
// addressed value binding: the frame slot starts each run from the
// bound value. A program that appends through &name gets the same
// answer every run. The appends of one run never reach the next.
func TestVarCellSeedsTheFrame(t *testing.T) {
	rt := NewRuntime()
	host := []string{"a"}
	seen := ""
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for name, v := range map[string]any{
		"args":   host,
		"append": Append,
		// SL_S and SS_E are in the table.
		"join":   func(sep string, v []string) string { return strings.Join(v, sep) },
		"record": func(a, b string) error { seen = a; return nil },
	} {
		must(rt.Bind(name, v))
	}

	src := "append(&args, \"-x\")\ns := join(\",\", args)\nrecord(s, \"\")"
	if err := rt.Supports(src); err != nil {
		t.Fatalf("expected the direct tier: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := fn.Exec[any](nil); err != nil {
			t.Fatal(err)
		}
		if seen != "a,-x" {
			t.Fatalf("run %d read %q, want a,-x on every run", i, seen)
		}
	}
	if strings.Join(host, ",") != "a" {
		t.Errorf("host reads %v, want its own header untouched", host)
	}
}

// TestVarCellFrameIsNotPooled pins the gate that keeps the per-run
// copy honest: because the cell's address goes to a callee, the frame
// escapes and the compiler leaves it out of the pool.
func TestVarCellFrameIsNotPooled(t *testing.T) {
	rt := NewRuntime()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for name, v := range map[string]any{
		"args":   []string{"a"},
		"append": Append,
		"join":   func(sep string, v []string) string { return strings.Join(v, sep) },
		"record": func(a, b string) error { return nil },
	} {
		must(rt.Bind(name, v))
	}

	prog, err := (&Parser{}).Parse("append(&args, \"-x\")\ns := join(\",\", args)\nrecord(s, \"\")")
	if err != nil {
		t.Fatal(err)
	}
	p, err := rt.compiler.compileProgram(prog)
	if err != nil {
		t.Fatal(err)
	}
	jp, err := jitCompileProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	if jp.pool != nil {
		t.Error("a frame whose cell address escapes must not be pooled")
	}
}
