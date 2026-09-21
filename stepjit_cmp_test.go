package gozero

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

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
		"signed ordering pair": {
			src:  `a := n8(-1); b := n8(1); s := ""; if a <= b { s = record("le"); }; return s;`,
			want: "le", seen: []string{"le", "le"},
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
