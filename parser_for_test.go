package gozero

import (
	"strings"
	"testing"
)

// TestParseFor pins the parsed shape of the condition and
// three-clause forms: which header field carries what, and the body
// as a nested statement list.
func TestParseFor(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		cond bool
		init string
		op   string
		dec  bool
		body int
	}{
		"cond name":     {src: `for done { poke() }`, cond: true, body: 1},
		"cond field":    {src: `for q.Open { poke() }`, cond: true, body: 1},
		"cond call":     {src: `for q.More() { poke(); poke() }`, cond: true, body: 2},
		"count up":      {src: `for i := 0; i < 3; i++ { poke() }`, init: "i", op: "<", body: 1},
		"count down":    {src: `for j := 9; j > 0; j-- { poke() }`, init: "j", op: ">", dec: true, body: 1},
		"not equal":     {src: `for i := 0; i != n; i++ { poke() }`, init: "i", op: "!=", body: 1},
		"lte":           {src: `for i := 0; i <= 3; i++ { poke() }`, init: "i", op: "<=", body: 1},
		"gte":           {src: `for i := 3; i >= 0; i-- { poke() }`, init: "i", op: ">=", dec: true, body: 1},
		"eq":            {src: `for i := 0; i == 0; i++ { break }`, init: "i", op: "==", body: 1},
		"call operands": {src: `for i := first(); i < last(); i++ { }`, init: "i", op: "<"},
		"neg literal":   {src: `for i := -3; i < -1; i++ { poke() }`, init: "i", op: "<", body: 1},
	} {
		prog, err := (&Parser{}).Parse(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		f := prog.stmts[0].fors
		if f == nil {
			t.Errorf("%s: not a for statement", name)
			continue
		}
		op := ""
		if f.cmp != nil {
			op = f.cmp.op
		}
		if (f.cond != nil) != tc.cond || f.initName != tc.init || op != tc.op || f.postDec != tc.dec || len(f.body) != tc.body {
			t.Errorf("%s: cond=%v init=%q op=%q dec=%v body=%d, want %v %q %q %v %d",
				name, f.cond != nil, f.initName, op, f.postDec, len(f.body), tc.cond, tc.init, tc.op, tc.dec, tc.body)
		}
	}
}

// TestParseForErrors pins the named header rules: no bare or constant
// condition, no comparison outside the three-clause header, all three
// clauses present, one name declared with :=, and the post clause
// stepping the loop variable.
func TestParseForErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"bare for":         {`for { poke() }`, "a bare for has no bound"},
		"constant cond":    {`for true { poke() }`, "a constant condition has no bound"},
		"literal cond":     {`for 1 { poke() }`, "a loop condition is a bool name, field or call"},
		"cond comparison":  {`for i < n { poke() }`, "a comparison stands only in the three-clause for header"},
		"assign init":      {`i := 0; for i = 0; i < 3; i++ { }`, "a three-clause init declares its name with :="},
		"two names":        {`for i, j := 0; i < 3; i++ { }`, "a three-clause init declares exactly one name"},
		"no comparison":    {`for i := 0; ; i++ { }`, "takes all three clauses"},
		"no relop":         {`for i := 0; i; i++ { }`, "takes one comparison"},
		"missing post sep": {`for i := 0; i < 3 { }`, "expected ';' after the comparison"},
		"post other name":  {`for i := 0; i < 3; j++ { }`, "the post clause steps the loop variable i"},
		"post no op":       {`for i := 0; i < 3; i { }`, "the post clause is i++ or i--"},
		"post missing":     {`for i := 0; i < 3; { }`, "the post clause is i++ or i--"},
		"unterminated":     {`for q.More() { poke();`, "unterminated block"},
	} {
		_, err := (&Parser{}).Parse(tc.src)
		if err == nil {
			t.Errorf("%s: parsed, want an error naming %q", name, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %q, want it to name %q", name, err, tc.want)
		}
	}
}

// TestParseForBody pins that a for body admits break and continue and
// rejects var and return, on the same depth rule every loop shares.
func TestParseForBody(t *testing.T) {
	for name, src := range map[string]string{
		"break":        `for q.More() { break }`,
		"continue":     `for i := 0; i < 3; i++ { continue }`,
		"nested":       `for i := 0; i < 3; i++ { for q.More() { break } }`,
		"range inside": `for q.More() { for x := range xs { rec(x) } }`,
		"for in range": `for x := range xs { for i := 0; i < 2; i++ { rec(x) } }`,
	} {
		if _, err := (&Parser{}).Parse(src); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, tc := range map[string]struct{ src, want string }{
		"var":    {`for i := 0; i < 3; i++ { var u url.URL }`, "var declaration cannot stand inside a loop body"},
		"return": {`for q.More() { return 1 }`, "return cannot stand inside a loop body"},
	} {
		_, err := (&Parser{}).Parse(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to name %q", name, err, tc.want)
		}
	}
}
