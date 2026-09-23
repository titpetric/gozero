package gozero

import (
	"io"
	"strings"
	"testing"
	"time"
)

// TestVarNodeTiers pins which value-binding reads the direct tier
// takes. A read is one load from a fixed address; a write is not in
// the table and must send the program to the reflect evaluator whole
// rather than lose the statement.
func TestVarNodeTiers(t *testing.T) {
	rt := NewRuntime()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	label := "hello"
	count := 0
	must(rt.BindVar("label", Mutable(&label)))
	must(rt.BindVar("count", Mutable(&count)))
	must(rt.BindVar("frozen", "constant"))
	must(rt.BindVar("dur", time.Hour))
	must(rt.BindVar("sentinel", io.EOF))
	// Shapes that are in the table, so the tier report is about the
	// argument rather than the signature.
	must(rt.Bind("pair", func(a, b string) error { return nil }))
	must(rt.Bind("countIn", func(p *int, s string) int64 { return int64(*p + len(s)) }))
	must(rt.Bind("takeAny", func(p *int, v any) error { return nil }))

	direct := []string{
		`pair(label, "x")`,
		`pair(frozen, "x")`,
		`n := countIn(&count, "x")`,
		`takeAny(&count, dur)`,
		`takeAny(&count, sentinel)`,
		`takeAny(&count, frozen)`,
	}
	for _, src := range direct {
		if err := rt.Supports(src); err != nil {
			t.Errorf("%q should be direct: %v", src, err)
		}
	}

	// A write has no node, and the refusal names itself so the
	// fallback is visible rather than silent.
	err := rt.Supports("count = 3")
	if err == nil || !strings.Contains(err.Error(), "not in the table") {
		t.Errorf("a write should report its fallback, got %v", err)
	}
}

// TestVarNodeReadsLiveStorage is the property the fixed-address load
// has to keep: a mutable binding reads the host's variable, not a
// copy taken when the program compiled.
func TestVarNodeReadsLiveStorage(t *testing.T) {
	rt := NewRuntime()
	seen := ""
	label := "before"
	if err := rt.BindVar("label", Mutable(&label)); err != nil {
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
		t.Fatalf("first read %q", seen)
	}
	label = "after"
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	if seen != "after" {
		t.Errorf("second read %q, want after: the node copied instead of loading", seen)
	}
}

// TestVarNodeMutableIfaceCopies pins the one place the direct tier
// must not alias. An immutable binding wider than a word can hand a
// callee a pointer to its own storage, because that storage never
// changes; a mutable one copies, or a later write would be visible
// through an interface a callee kept.
func TestVarNodeMutableIfaceCopies(t *testing.T) {
	rt := NewRuntime()
	var held any
	anchor := 0
	if err := rt.Bind("keep", func(p *int, v any) error { held = copyBoxed(v); return nil }); err != nil {
		t.Fatal(err)
	}
	label := "before"
	if err := rt.BindVar("label", Mutable(&label)); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindVar("anchor", Mutable(&anchor)); err != nil {
		t.Fatal(err)
	}
	src := `keep(&anchor, label)`
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
	label = "after"
	if held != "before" {
		t.Errorf("the callee's copy reads %v, want before", held)
	}
}

// TestVarCellSeedsTheFrame covers the direct tier's prologue for an
// addressed immutable binding: the frame slot starts each run from the
// bound value, so a program that appends through &name gets the same
// answer every run rather than accumulating across them.
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
	must(rt.BindVar("args", host))
	must(rt.Bind("append", Append))
	// PS_i64 and SS_E are in the table, so the tier report is about
	// the addressed binding rather than the recorder's signature.
	must(rt.Bind("join", func(sep string, v []string) string { return strings.Join(v, sep) }))
	must(rt.Bind("record", func(a, b string) error { seen = a; return nil }))

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
// copy honest: the cell's address goes to a callee, so the frame
// escapes and cannot be recycled between runs.
func TestVarCellFrameIsNotPooled(t *testing.T) {
	rt := NewRuntime()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(rt.BindVar("args", []string{"a"}))
	must(rt.Bind("append", Append))
	must(rt.Bind("join", func(sep string, v []string) string { return strings.Join(v, sep) }))
	must(rt.Bind("record", func(a, b string) error { return nil }))

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
