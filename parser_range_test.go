package gozero

import (
	"strings"
	"testing"
)

// TestParseRange pins the range statement's parsed shape: the header
// forms, the sources a header accepts, and the body as a nested
// statement list. A composite literal holds back in the header, so
// the brace after the ranged expression opens the body.
func TestParseRange(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		key  string
		val  string
		body int
	}{
		"two names":     {src: `for k, v := range m { poke(); poke() }`, key: "k", val: "v", body: 2},
		"one name":      {src: `for x := range xs { poke() }`, key: "x", body: 1},
		"blank key":     {src: `for _, v := range xs { poke() }`, key: "_", val: "v", body: 1},
		"bare":          {src: `for range xs { poke() }`, body: 1},
		"integer bound": {src: `for i := range 3 { poke() }`, key: "i", body: 1},
		"call source":   {src: `for s := range fields("x") { poke() }`, key: "s", body: 1},
		"field source":  {src: `for _, c := range req.Cookies { poke() }`, val: "c", key: "_", body: 1},
		"composite in a call argument": {
			src: `for _, s := range names(url.URL{Path: "/"}) { poke() }`, key: "_", val: "s", body: 1,
		},
		"empty body":  {src: `for range xs { }`},
		"nested loop": {src: `for range xs { for range ys { poke() } }`, body: 1},
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
// range body, close like any statement, and take no label.
func TestParseLoopExit(t *testing.T) {
	for name, src := range map[string]string{
		"break":              `for range xs { break }`,
		"continue":           `for range xs { continue }`,
		"break terminated":   `for range xs { break; poke() }`,
		"continue then line": "for range xs {\n\tcontinue\n\tpoke()\n}",
		"nested break":       `for range xs { for range ys { break } }`,
		"break before brace": `for range xs { poke(); break }`,
		"break inside an if": `for range xs { if ok { break } }`,
	} {
		if _, err := (&Parser{}).Parse(src); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	for name, tc := range map[string]struct{ src, want string }{
		"break at top":      {`break`, "only allowed inside a range body"},
		"continue at top":   {`continue`, "only allowed inside a range body"},
		"break label":       {`for range xs { break out }`, "a label after break is not in the language"},
		"continue label":    {`for range xs { continue out }`, "a label after continue is not in the language"},
		"break after loop":  {`for range xs { poke() }; break`, "only allowed inside a range body"},
		"break in an arm":   {`if ok { break }`, "only allowed inside a range body"},
		"return in a body":  {`for range xs { return }`, "return cannot stand inside a range body"},
		"var in a body":     {`for range xs { var u url.URL }`, "var declaration cannot stand inside a range body"},
		"return in an arm":  {`for range xs { if ok { return } }`, "return cannot stand inside a range body"},
		"not the range form": {`for poke() { }`, "only the range form"},
		"assign form":        {`for i = range xs { poke() }`, "declares its names with :="},
		"three names":        {`for a, b, c := range xs { poke() }`, "at most two names"},
		"literal source":     {`for s := range "ab" { poke() }`, "cannot range over this expression"},
		"unterminated":       {`for range xs { poke();`, "unterminated block"},
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
