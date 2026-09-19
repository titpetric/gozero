package gozero

import (
	"strings"
	"testing"
)

// TestParseRange pins the range statement's parsed shape: the header
// forms, the sources a header accepts, and the body as a nested
// statement list.
func TestParseRange(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		key  string
		val  string
		body int
	}{
		"two names":      {src: `for k, v := range m { poke(); poke() }`, key: "k", val: "v", body: 2},
		"one name":       {src: `for x := range xs { poke() }`, key: "x", body: 1},
		"blank key":      {src: `for _, v := range xs { poke() }`, key: "_", val: "v", body: 1},
		"bare":           {src: `for range xs { poke() }`, body: 1},
		"string literal": {src: `for i, r := range "ab" { poke() }`, key: "i", val: "r", body: 1},
		"integer bound":  {src: `for i := range 3 { poke() }`, key: "i", body: 1},
		"call source":    {src: `for s := range lines("x") { poke() }`, key: "s", body: 1},
		"empty body":     {src: `for range xs { }`},
	} {
		prog, err := (&Parser{}).Parse(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		r := prog.stmts[0].rng
		if r == nil {
			t.Errorf("%s: not a range statement", name)
			continue
		}
		if r.key != tc.key || r.val != tc.val || len(r.body) != tc.body {
			t.Errorf("%s: key=%q val=%q body=%d, want %q %q %d", name, r.key, r.val, len(r.body), tc.key, tc.val, tc.body)
		}
	}
}

// TestParseLoopExit pins break and continue: both parse only inside a
// loop body, close like any statement, and take no label.
func TestParseLoopExit(t *testing.T) {
	for name, src := range map[string]string{
		"break":               `for range xs { break }`,
		"continue":            `for range xs { continue }`,
		"break terminated":    `for range xs { break; poke() }`,
		"continue then line":  "for range xs {\n\tcontinue\n\tpoke()\n}",
		"nested break":        `for range xs { for range ys { break } }`,
		"break before brace":  `for range xs { poke(); break }`,
	} {
		if _, err := (&Parser{}).Parse(src); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	for name, tc := range map[string]struct{ src, want string }{
		"break at top":     {`break`, "only allowed inside a loop body"},
		"continue at top":  {`continue`, "only allowed inside a loop body"},
		"break label":      {`for range xs { break out }`, "a label after break is not in the language"},
		"continue label":   {`for range xs { continue out }`, "a label after continue is not in the language"},
		"break after loop": {`for range xs { poke() }; break`, "only allowed inside a loop body"},
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
