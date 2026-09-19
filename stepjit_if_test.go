package gozero

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ifPairRuntime is the binding surface the equivalence tests run: a
// bool source, a recorder for observable effects, and a call whose
// trailing error ends the program from inside a condition.
func ifPairRuntime(t *testing.T, seen *[]string) *Runtime {
	t.Helper()
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"strings.HasPrefix": strings.HasPrefix,
		"http.NewRequest":   http.NewRequest,
		"yes":               func(s string) bool { return true },
		"no":                func(s string) bool { return false },
		"record":            func(s string) string { *seen = append(*seen, s); return s },
		"boom":              func() (bool, error) { return true, errors.New("boom") },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	return rt
}

// TestIfMatchesReflect runs the same if programs down both tiers and
// requires identical results, identical recorded effects, and errors
// on the same programs. This is the equivalence contract for the
// construct: the reflect evaluator is the semantic reference and the
// structured if node must not diverge from it.
func TestIfMatchesReflect(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want any
		seen []string
		err  bool
	}{
		"then arm": {
			src:  `ok := yes(""); s := ""; if ok { s = record("then"); } else { s = record("else"); }; return s;`,
			want: "then", seen: []string{"then", "then"},
		},
		"else arm": {
			src:  `ok := no(""); s := ""; if ok { s = record("then"); } else { s = record("else"); }; return s;`,
			want: "else", seen: []string{"else", "else"},
		},
		"else if middle": {
			src:  "a := no(\"\")\nb := yes(\"\")\ns := \"\"\nif a {\n\ts = record(\"a\")\n} else if b {\n\ts = record(\"b\")\n} else {\n\ts = record(\"c\")\n}\nreturn s\n",
			want: "b", seen: []string{"b", "b"},
		},
		"call cond": {
			src:  `s := ""; if strings.HasPrefix("/api/x", "/api") { s = record("hit"); }; return s;`,
			want: "hit", seen: []string{"hit", "hit"},
		},
		"field cond": {
			src:  "req := http.NewRequest(\"GET\", \"https://h/p\")\ns := \"alive\"\nif req.Close {\n\ts = record(\"close\")\n}\nreturn s\n",
			want: "alive", seen: []string{},
		},
		"nested": {
			src:  "s := \"\"\nif yes(\"\") {\n\trecord(\"outer\")\n\tif no(\"\") {\n\t\ts = record(\"inner\")\n\t} else {\n\t\ts = record(\"deep\")\n\t}\n}\nreturn s\n",
			want: "deep", seen: []string{"outer", "deep", "outer", "deep"},
		},
		"skipped arm leaves zero": {
			src:  `s := ""; if no("") { s = record("set"); }; return s;`,
			want: "", seen: []string{},
		},
		"statements after the join": {
			src:  "s := \"\"\nif yes(\"\") {\n\ts = \"x\"\n}\nrecord(s)\nreturn s\n",
			want: "x", seen: []string{"x", "x"},
		},
		"cond error ends the program": {
			src: `if boom() { record("never"); }; record("tail");`,
			err: true, seen: []string{},
		},
		"arm error ends the program": {
			src: `if yes("") { boom(); record("never"); }; record("tail");`,
			err: true, seen: []string{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			rt := ifPairRuntime(t, &seen)
			jit, slow := compilePair(t, rt, tc.src)
			for tier, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
				got, err := fn(t.Context(), nil, nil)
				if tc.err {
					if err == nil {
						t.Errorf("%s: expected an error", tier)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s: %v", tier, err)
				}
				if got != tc.want {
					t.Errorf("%s: got %v, want %v", tier, got, tc.want)
				}
			}
			if fmt.Sprint(seen) != fmt.Sprint(tc.seen) {
				t.Errorf("effects %v, want %v", seen, tc.seen)
			}
		})
	}
}

// cmpPairRuntime binds scalar sources at the widths the comparison
// rung has to get right, a recorder, and the time scope the duration
// comparisons read.
func cmpPairRuntime(t *testing.T, seen *[]string) *Runtime {
	t.Helper()
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"n8":         func(v int64) int8 { return int8(v) },
		"u8":         func(v int64) uint8 { return uint8(v) },
		"n64":        func(v int64) int64 { return v },
		"f32":        func(v float64) float32 { return float32(v) },
		"str":        func(s string) string { return s },
		"yes":        func() bool { return true },
		"record":     func(s string) string { *seen = append(*seen, s); return s },
		"boom":       func() (int64, error) { return 0, errors.New("boom") },
		"time.Now":   time.Now,
		"time.Since": time.Since,
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.BindValue("time.Hour", time.Hour); err != nil {
		t.Fatal(err)
	}
	return rt
}

// TestCmpMatchesReflect runs the same comparison programs down both
// tiers and requires identical results, identical recorded effects,
// and errors on the same programs. The signed cases matter most: the
// direct tier carries narrow integers zero-extended, and a
// comparison that forgets to sign-extend calls int8(-1) bigger than
// zero.
func TestCmpMatchesReflect(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want any
		seen []string
		err  bool
	}{
		"int literal": {
			src:  `n := n64(200); s := ""; if n == 200 { s = record("eq"); } else { s = record("ne"); }; return s;`,
			want: "eq", seen: []string{"eq", "eq"},
		},
		"literal left": {
			src:  `n := n64(200); s := ""; if 500 > n { s = record("lt"); }; return s;`,
			want: "lt", seen: []string{"lt", "lt"},
		},
		"signed sign extension": {
			src:  `a := n8(-1); s := "pos"; if a < 0 { s = record("neg"); }; return s;`,
			want: "neg", seen: []string{"neg", "neg"},
		},
		"signed equality": {
			src:  `a := n8(-5); b := n8(-5); s := ""; if a == b { s = record("same"); }; return s;`,
			want: "same", seen: []string{"same", "same"},
		},
		"unsigned width": {
			src:  `a := u8(200); s := ""; if a > 100 { s = record("big"); }; return s;`,
			want: "big", seen: []string{"big", "big"},
		},
		"string order": {
			src:  `a := str("abc"); s := ""; if a < "abd" { s = record("lt"); }; return s;`,
			want: "lt", seen: []string{"lt", "lt"},
		},
		"float32 exact": {
			src:  `x := f32(1.5); s := ""; if x >= 1.5 { s = record("ge"); }; return s;`,
			want: "ge", seen: []string{"ge", "ge"},
		},
		"bool equality": {
			src:  `ok := yes(); s := ""; if ok == false { s = record("f"); } else { s = record("t"); }; return s;`,
			want: "t", seen: []string{"t", "t"},
		},
		"rhs call": {
			src:  `n := n64(3); s := ""; if n != n64(4) { s = record("ne"); }; return s;`,
			want: "ne", seen: []string{"ne", "ne"},
		},
		"duration since": {
			src:  `t := time.Now(); s := "stale"; if time.Since(t) < time.Hour { s = record("fresh"); }; return s;`,
			want: "fresh", seen: []string{"fresh", "fresh"},
		},
		// The two spellings below only parse because go/parser reads
		// the header; the walk hands both to the same comparison
		// compile, so both tiers must agree on them too.
		"hex literal": {
			src:  `n := n64(255); s := ""; if n == 0xFF { s = record("hex"); }; return s;`,
			want: "hex", seen: []string{"hex", "hex"},
		},
		"paren header": {
			src:  `n := n64(2); s := ""; if (n > 1) { s = record("gt"); }; return s;`,
			want: "gt", seen: []string{"gt", "gt"},
		},
		"operand error ends the program": {
			src: `s := ""; if boom() == 1 { s = record("never"); }; record("tail");`,
			err: true, seen: []string{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			rt := cmpPairRuntime(t, &seen)
			jit, slow := compilePair(t, rt, tc.src)
			for tier, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
				got, err := fn(t.Context(), nil, nil)
				if tc.err {
					if err == nil {
						t.Errorf("%s: expected an error", tier)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s: %v", tier, err)
				}
				if got != tc.want {
					t.Errorf("%s: got %v, want %v", tier, got, tc.want)
				}
			}
			if fmt.Sprint(seen) != fmt.Sprint(tc.seen) {
				t.Errorf("effects %v, want %v", seen, tc.seen)
			}
		})
	}
}

// TestCmpSupports pins the tiering: a scalar comparison program is
// fully direct, and a program reaching through time.Time still
// compiles to the direct tier with the two struct calls named as
// bridges, because time.Time has no layout class.
func TestCmpSupports(t *testing.T) {
	var seen []string
	rt := cmpPairRuntime(t, &seen)
	if err := rt.Supports(`n := 200; s := ""; if n > 100 { s = "y"; }; return s;`); err != nil {
		t.Errorf("a comparison program should be direct: %v", err)
	}
	src := `t := time.Now(); s := "stale"; if time.Since(t) < time.Hour { s = "fresh"; }; return s;`
	err := rt.Supports(src)
	if err == nil || !strings.Contains(err.Error(), "time.Now") {
		t.Errorf("time.Now should be a named bridge, got %v", err)
	}
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	p, err := rt.compiler.compileProgram(prog)
	if err != nil {
		t.Fatal(err)
	}
	jp, err := jitCompileProgram(p)
	if err != nil {
		t.Fatalf("the program should stay on the direct tier: %v", err)
	}
	if len(jp.bridged) != 2 {
		t.Errorf("bridged = %v, want time.Now and time.Since", jp.bridged)
	}
}

// BenchmarkCmpHeader prices the comparison node against the L1 way
// of writing the same guard, a bound bool predicate. Same program
// shape, same result; the delta is one direct call replaced by two
// loads and a compare.
func BenchmarkCmpHeader(b *testing.B) {
	rt := NewRuntime()
	if err := rt.Bind("gt1", func(v int64) bool { return v > 1 }); err != nil {
		b.Fatal(err)
	}
	for name, src := range map[string]string{
		"predicate":  `n := 2; s := "a"; if gt1(n) { s = "b"; }; return s;`,
		"comparison": `n := 2; s := "a"; if n > 1 { s = "b"; }; return s;`,
	} {
		if err := rt.Supports(src); err != nil {
			b.Fatalf("%s should be direct: %v", name, err)
		}
		fn, err := rt.Compile(src)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := fn.ExecContext[any](b.Context(), nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestIfWritesConservative pins the planner's counting: a slot
// assigned inside an arm is maybe-written, so it counts as more than
// one write and the write-once interface aliasing stays off for it.
// A slot written only before the if keeps its single count.
func TestIfWritesConservative(t *testing.T) {
	var seen []string
	rt := ifPairRuntime(t, &seen)
	prog, err := (&Parser{}).Parse(`once := record("o"); s := ""; if yes("") { s = "x"; }; record(s); record(once);`)
	if err != nil {
		t.Fatal(err)
	}
	p, err := rt.compiler.compileProgram(prog)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planIf(p)
	if err != nil {
		t.Fatal(err)
	}
	onceSlot, sSlot := p.stmts[0].out[0], p.stmts[1].out[0]
	if got := plan.writes[onceSlot]; got != 1 {
		t.Errorf("writes[once] = %d, want 1", got)
	}
	if got := plan.writes[sSlot]; got < 2 {
		t.Errorf("writes[s] = %d, want at least 2: a maybe-written slot must not alias", got)
	}
}

// TestIfSupports pins Supports on the construct: a full if program
// reports as direct, and a mid-program return stays named.
func TestIfSupports(t *testing.T) {
	var seen []string
	rt := ifPairRuntime(t, &seen)
	if err := rt.Supports(`s := ""; if strings.HasPrefix("/a", "/") { s = "y"; }; return s;`); err != nil {
		t.Errorf("an if program should be direct: %v", err)
	}
	err := rt.Supports("ok := strings.HasPrefix(\"/a\", \"/\")\nreturn ok\nstrings.HasPrefix(\"/b\", \"/\")\n")
	if err == nil || !strings.Contains(err.Error(), "straight line") {
		t.Errorf("a mid-program return should stay named, got %v", err)
	}
}

// BenchmarkIfWriteCount measures what the conservative write count
// costs: the same string reaching the same interface parameters,
// written once in a straight line against maybe-written in an arm.
// The aliased form points the interfaces at the frame, which stops
// frame pooling; the conservative form boxes the string per read and
// keeps the pooled frame. The delta between the two is the price of
// a branch touching the name.
func BenchmarkIfWriteCount(b *testing.B) {
	rt := NewRuntime()
	if err := rt.BindScope("assert", map[string]any{"Equal": assertEqual}); err != nil {
		b.Fatal(err)
	}
	for name, src := range map[string]string{
		"aliased":      `s := "abc"; assert.Equal(tb, s, s);`,
		"conservative": "var ok bool\ns := \"abc\"\nif ok {\n\ts = \"xyz\"\n}\nassert.Equal(tb, s, s)\n",
	} {
		if err := rt.Supports(src); err != nil {
			b.Fatalf("%s should be direct: %v", name, err)
		}
		fn, err := rt.Compile(src)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(name, func(b *testing.B) {
			stack := map[string]any{"tb": b}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := fn.ExecContext[any](b.Context(), stack); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
