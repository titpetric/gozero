package gozero

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// TestValueStructInFrame pins the struct-by-value slot support: the
// frame holds the struct itself, its zero value is the zeroed frame,
// a field read is an offset from the frame pointer and a field write
// stores through the same offset. Both tiers must agree.
func TestValueStructInFrame(t *testing.T) {
	rt := pairRuntime(t)
	const src = `
		var u url.URL;
		u.Path = "/in-frame";
		u.Scheme = "https";
		json.NewEncoder(dest).Encode(u.Path);
	`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("a value struct var should now JIT: %v", err)
	}
	jit, slow := compilePair(t, rt, src)
	var a, b bytes.Buffer
	if _, err := jit(context.Background(), nil, &a); err != nil {
		t.Fatal(err)
	}
	if _, err := slow(context.Background(), nil, &b); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() || a.String() != "\"/in-frame\"\n" {
		t.Errorf("dest = %q (jit) vs %q (reflect)", a.String(), b.String())
	}
}

// fieldBox is a struct with one field per store class the field-write
// tests need: only string and pointer fields had coverage.
type fieldBox struct {
	N     int64
	F     float64
	S     string
	Parts []string
	V     any
}

// boxRuntime binds constructors for fieldBox, including one that
// returns a nil pointer for the nil-base guards.
func boxRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"newBox": func() *fieldBox { return &fieldBox{} },
		"nilBox": func() *fieldBox { return nil },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	for name, fns := range map[string]map[string]any{
		"json":    {"NewEncoder": json.NewEncoder},
		"strings": {"Fields": strings.Fields},
		"url":     {"Parse": url.Parse},
	} {
		if err := rt.BindScope(name, fns); err != nil {
			t.Fatal(err)
		}
	}
	return rt
}

// TestFieldWriteClasses covers the stores fieldSetNode compiles beyond
// string and pointer: a scalar, a float, a slice from a call result and
// an interface from a literal, read back through Encode on the same
// run.
func TestFieldWriteClasses(t *testing.T) {
	rt := boxRuntime(t)
	for name, tc := range map[string]struct{ src, want string }{
		"scalar": {`b := newBox(); b.N = 41; json.NewEncoder(dest).Encode(b.N)`, "41\n"},
		"float":  {`b := newBox(); b.F = 2.5; json.NewEncoder(dest).Encode(b.F)`, "2.5\n"},
		"slice":  {`b := newBox(); b.Parts = strings.Fields("a b"); json.NewEncoder(dest).Encode(b.Parts)`, "[\"a\",\"b\"]\n"},
		"iface":  {`b := newBox(); b.V = "boxed"; json.NewEncoder(dest).Encode(b.V)`, "\"boxed\"\n"},
		"string": {`b := newBox(); b.S = "s"; json.NewEncoder(dest).Encode(b.S)`, "\"s\"\n"},
	} {
		fn, err := rt.Compile(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var dest bytes.Buffer
		if err := fn.Scan(&dest, nil); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if dest.String() != tc.want {
			t.Errorf("%s: dest = %q, want %q", name, dest.String(), tc.want)
		}
	}
}

// TestFieldWriteNilBase pins the run-time guard: writing a field of a
// nil pointer is an error naming the base, not a fault.
func TestFieldWriteNilBase(t *testing.T) {
	rt := boxRuntime(t)
	fn, err := rt.Compile(`b := nilBox(); b.N = 5; json.NewEncoder(dest).Encode("never")`)
	if err != nil {
		t.Fatal(err)
	}
	var dest bytes.Buffer
	err = fn.Scan(&dest, nil)
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("err = %v, want the nil write reported", err)
	}
}

// TestDiscardedResults covers dropNode for the classes nothing
// discarded before: a string from a chained method, a slice, a scalar
// and an interface, each as a bare statement.
func TestDiscardedResults(t *testing.T) {
	rt := boxRuntime(t)
	if err := rt.Bind("plen", func(u *url.URL) int64 { return int64(len(u.Path)) }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("val", func() any { return "v" }); err != nil {
		t.Fatal(err)
	}
	const src = `
		u := url.Parse("/drop")
		u.String()
		strings.Fields("a b")
		plen(u)
		val()
		json.NewEncoder(dest).Encode(u.Path)
	`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	var dest bytes.Buffer
	if err := fn.Scan(&dest, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := dest.String(), "\"/drop\"\n"; got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}
}

// TestInterfaceSlotStore pins an interface value written into a frame
// slot: reading the name twice stops planInline splicing the producer
// away, so the pair really is stored and loaded.
func TestInterfaceSlotStore(t *testing.T) {
	rt := boxRuntime(t)
	var got []any
	if err := rt.Bind("take", func(v any) *fieldBox {
		got = append(got, v)
		return &fieldBox{}
	}); err != nil {
		t.Fatal(err)
	}
	const src = `
		v := val()
		take(v)
		take(v)
		json.NewEncoder(dest).Encode("done")
	`
	if err := rt.Bind("val", func() any { return "stored" }); err != nil {
		t.Fatal(err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	var dest bytes.Buffer
	if err := fn.Scan(&dest, nil); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "stored" || got[1] != "stored" {
		t.Errorf("callee saw %v, want stored twice", got)
	}
}
