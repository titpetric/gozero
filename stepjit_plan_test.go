package gozero

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestPlanInlineLeavesTheTreeAlone guards the interaction between the
// tiers. planInline decides a value can travel as a return value
// instead of through a slot, which drops the statement that produced
// it. That decision lives in a side table: if it edited the call tree,
// the reflect evaluator, which runs the same tree when the whole
// program declines, would see the producer both spliced into its
// reader and standing as its own statement, and run it twice. The
// bridge has the mirror obligation: a bridged reader must follow the
// splice to the producer's node rather than reading the dropped slot.
func TestPlanInlineLeavesTheTreeAlone(t *testing.T) {
	rt := pairRuntime(t)
	calls := 0
	if err := rt.Bind("once", func(s string) (*url.URL, error) {
		calls++
		return &url.URL{Path: s}, nil
	}); err != nil {
		t.Fatal(err)
	}

	// var u url.URL keeps this program on the reflect evaluator: a
	// value-struct slot has no layout class.
	const fallback = `
		u := once("/a");
		s := u.String();
		var w url.URL;
		json.NewEncoder(dest).Encode(s);
	`
	fn, err := rt.Compile(fallback)
	if err != nil {
		t.Fatal(err)
	}
	var dest bytes.Buffer
	if err := fn.Scan(&dest, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("reflect tier: once called %d times, want 1", calls)
	}
	if got, want := dest.String(), "\"/a\"\n"; got != want {
		t.Errorf("reflect tier: dest = %q, want %q", got, want)
	}

	// The same shape with a bridged reader, which must see the spliced
	// producer exactly once through the side table.
	if err := rt.Bind("reader", func(u *url.URL, a, b, c, d string) (*url.URL, error) {
		return &url.URL{Path: u.Path + a + b + c + d}, nil
	}); err != nil {
		t.Fatal(err)
	}
	const bridged = `
		u := once("/b");
		r := reader(u, "1", "2", "3", "4");
		json.NewEncoder(dest).Encode(r.Path);
	`
	fn, err = rt.Compile(bridged)
	if err != nil {
		t.Fatal(err)
	}
	calls = 0
	dest.Reset()
	if err := fn.Scan(&dest, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("bridged tier: once called %d times, want 1", calls)
	}
	if got, want := dest.String(), "\"/b1234\"\n"; got != want {
		t.Errorf("bridged tier: dest = %q, want %q", got, want)
	}
}

// TestPlanInlineDeclinesMidReturn pins the early-exit rule for the two
// return forms planInline used to wave through: "return name;" and a
// bare "return;". Both recorded the exit and kept planning, so the
// direct tier ran the statements after the return, which the reflect
// evaluator never reaches. Such a program declines by name and runs on
// the reflect tier, where the tail must stay unexecuted.
func TestPlanInlineDeclinesMidReturn(t *testing.T) {
	newRuntime := func(t *testing.T, ran *[]string) *Runtime {
		t.Helper()
		rt := NewRuntime()
		if err := rt.Bind("record", func(tag, val string) error {
			*ran = append(*ran, tag)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return rt
	}

	t.Run("value form", func(t *testing.T) {
		var ran []string
		rt := newRuntime(t, &ran)
		src := "s := \"a\"\nreturn s\nrecord(\"tail\", \"x\")\n"
		if err := rt.Supports(src); err == nil || !strings.Contains(err.Error(), "straight line") {
			t.Errorf("a value return before the last statement should decline by name, got %v", err)
		}
		fn, err := rt.Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		got, err := fn.Exec[string](nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != "a" {
			t.Errorf("result = %q, want %q", got, "a")
		}
		if len(ran) != 0 {
			t.Errorf("the tail after the return ran: %v", ran)
		}
	})

	t.Run("bare form", func(t *testing.T) {
		var ran []string
		rt := newRuntime(t, &ran)
		src := "record(\"head\", \"x\")\nreturn\nrecord(\"tail\", \"y\")\n"
		if err := rt.Supports(src); err == nil || !strings.Contains(err.Error(), "straight line") {
			t.Errorf("a bare return before the last statement should decline by name, got %v", err)
		}
		fn, err := rt.Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fn(context.Background(), nil, nil); err != nil {
			t.Fatal(err)
		}
		if want := []string{"head"}; len(ran) != 1 || ran[0] != want[0] {
			t.Errorf("ran = %v, want %v", ran, want)
		}
	})
}

// TestReadCountSeesFieldReads guards the analysis that decides whether a
// producer can be spliced into its reader and its slot dropped.
//
// req.Header is a read of req. Missing it undercounts, and a name read
// once directly and once through a field then looked read-once: the
// producer was spliced into the direct read and its statement dropped,
// leaving the field read pointing at a slot nothing writes. It failed
// safe only because the argument compiler happened to report the
// missing slot, which is not a guarantee.
func TestReadCountSeesFieldReads(t *testing.T) {
	rt := pairRuntime(t)
	if err := rt.Bind("both", func(r *http.Request, h *url.URL) (*url.URL, error) {
		return &url.URL{Path: r.URL.Path + "|" + h.Path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	const src = `
		req := http.NewRequest("GET", "/p");
		u := both(req, req.URL);
		json.NewEncoder(dest).Encode(u.Path);
	`
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	p, err := rt.compiler.compileProgram(prog)
	if err != nil {
		t.Fatal(err)
	}
	reads := map[int]int{}
	for i := range p.stmts {
		if p.stmts[i].call != nil {
			countReads(reads, p.stmts[i].call)
		}
	}
	if reads[0] != 2 {
		t.Errorf("req counted %d reads, want 2 (one direct, one through a field)", reads[0])
	}

	jit, slow := compilePair(t, rt, src)
	var a, b bytes.Buffer
	if _, err := jit(context.Background(), nil, &a); err != nil {
		t.Fatal(err)
	}
	if _, err := slow(context.Background(), nil, &b); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Errorf("%q (jit) vs %q (reflect)", a.String(), b.String())
	}
	if got, want := a.String(), "\"/p|/p\"\n"; got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}
}
