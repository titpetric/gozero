package gozero

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
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
		"mark":              func(s string) { *seen = append(*seen, s) },
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
		"step inside an arm": {
			src:  "n := 0\nif yes(\"\") {\n\tn++\n\tn++\n}\nreturn n\n",
			want: int64(2), seen: []string{},
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

// TestIfReturnMatchesReflect is the equivalence contract for the
// return signal: the same programs return the same value down both
// tiers, and the statements after a return never run on either. The
// recorded effects are what proves the second half, since a skipped
// statement leaves no value behind to compare.
func TestIfReturnMatchesReflect(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want any
		seen []string
	}{
		"from the then arm": {
			src:  `s := record("v"); if yes("") { return s; }; mark("tail");`,
			want: "v", seen: []string{"v", "v"},
		},
		"from the else arm": {
			src:  `s := record("v"); if no("") { mark("then"); } else { return s; }; mark("tail");`,
			want: "v", seen: []string{"v", "v"},
		},
		"not taken, falls through": {
			src:  `s := record("v"); if no("") { return s; }; mark("tail"); return s;`,
			want: "v", seen: []string{"v", "tail", "v", "tail"},
		},
		"from a nested if": {
			src:  "s := record(\"v\")\nif yes(\"\") {\n\tif yes(\"\") {\n\t\treturn s\n\t}\n\tmark(\"unreached\")\n}\nmark(\"tail\")\n",
			want: "v", seen: []string{"v", "v"},
		},
		"mid-arm, trailing statements skipped": {
			src:  "s := record(\"v\")\nif yes(\"\") {\n\ts = record(\"early\")\n\treturn s\n\tmark(\"never\")\n}\nmark(\"tail\")\n",
			want: "early", seen: []string{"v", "early", "v", "early"},
		},
		"bare return from an arm": {
			src:  `record("v"); if yes("") { return; }; mark("tail");`,
			want: nil, seen: []string{"v", "v"},
		},
		"valueless call then return": {
			src:  `if yes("") { mark("effect"); return; }; mark("tail");`,
			want: nil, seen: []string{"effect", "effect"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			rt := ifPairRuntime(t, &seen)
			jit, slow := compilePair(t, rt, tc.src)
			for _, tier := range []string{"jit", "reflect"} {
				fn := jit
				if tier == "reflect" {
					fn = slow
				}
				got, err := fn(t.Context(), nil, nil)
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
	plan, err := planBlocks(p)
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
	if plan.retSignal {
		t.Error("a program with no return inside an arm should not ask for the signal field")
	}
}

// TestIfSupports pins Supports on the construct: a full if program
// reports as direct, one returning from an arm does too, and the
// named rejections stay named.
func TestIfSupports(t *testing.T) {
	var seen []string
	rt := ifPairRuntime(t, &seen)
	for name, src := range map[string]string{
		"assignment in an arm": `s := ""; if strings.HasPrefix("/a", "/") { s = "y"; }; return s;`,
		"return from an arm":   `s := ""; if strings.HasPrefix("/a", "/") { return s; }; s = "y"; return s;`,
		"bare return":          `s := ""; if strings.HasPrefix("/a", "/") { return; }; s = "y";`,
	} {
		if err := rt.Supports(src); err != nil {
			t.Errorf("%s should be direct: %v", name, err)
		}
	}
	for name, tc := range map[string]struct {
		src  string
		want string
	}{
		"mid-program return": {
			"ok := strings.HasPrefix(\"/a\", \"/\")\nreturn ok\nstrings.HasPrefix(\"/b\", \"/\")\n",
			"straight line",
		},
		"returned call value in an arm": {
			`if strings.HasPrefix("/a", "/") { return strings.HasPrefix("/b", "/"); };`,
			"a returned call value inside an if arm is not in the table",
		},
	} {
		err := rt.Supports(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to name %q", name, err, tc.want)
		}
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
