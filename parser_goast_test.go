package gozero

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// TestParseRange pins the range statement's parsed shape: the header
// forms, the sources a header accepts, and the body as a nested
// statement list. The statement reaches the same rangeStmt as before,
// but through go/parser and the ast lowering.
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
		"chained call":   {src: `for s := range mk("x").Lines() { poke() }`, key: "s", body: 1},
		"paren source":   {src: `for s := range (xs) { poke() }`, key: "s", body: 1},
		"field source":   {src: `for s := range w.In { poke() }`, key: "s", body: 1},
		"empty body":     {src: `for range xs { }`},
		"stray semis":    {src: `for range xs { ;; poke(); }`, body: 1},
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

// TestParseLoopExtent pins the seam between the two parsers: the
// go/scanner extent hands exactly the loop to go/parser, and the
// hand-rolled parser resumes right after the closing brace.
func TestParseLoopExtent(t *testing.T) {
	for name, tc := range map[string]struct {
		src   string
		stmts int
	}{
		"loop then stmt":     {src: "for range xs { poke() }\npoke()", stmts: 2},
		"loop semi stmt":     {src: `for range xs { poke() }; poke()`, stmts: 2},
		"brace in string":    {src: "for range xs { rec(\"}\") }\npoke()", stmts: 2},
		"brace in comment":   {src: "for range xs { // }\npoke() }\npoke()", stmts: 2},
		"paren brace header": {src: "for range f(g()) { poke() }", stmts: 1},
		"nested loops":       {src: "for range xs { for range ys { poke() } }\npoke()", stmts: 2},
	} {
		prog, err := (&Parser{}).Parse(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(prog.stmts) != tc.stmts {
			t.Errorf("%s: %d statements, want %d", name, len(prog.stmts), tc.stmts)
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
	} {
		if _, err := (&Parser{}).Parse(src); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	for name, tc := range map[string]struct{ src, want string }{
		"break at top":     {`break`, "only allowed inside a range body"},
		"continue at top":  {`continue`, "only allowed inside a range body"},
		"break label":      {`for range xs { break out }`, "a label after break is not in the language"},
		"continue label":   {`for range xs { continue out }`, "a label after continue is not in the language"},
		"break after loop": {`for range xs { poke() }; break`, "only allowed inside a range body"},
		"goto":             {`for range xs { goto out }`, "goto is not in the language"},
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

// TestParseLoopErrors pins what the delegated front end rejects and
// whose voice the message speaks in. Go grammar violations surface
// go/parser's message with the offset mapped back into the program;
// house rules keep their house message; a body statement's error
// comes from the hand-rolled parser at its true offset.
func TestParseLoopErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"three names":     {`for a, b, c := range 3 { poke() }`, "expected at most 2 expressions"},
		"assign form":     {`for i = range 3 { poke() }`, "declares its names with :="},
		"cond form":       {`for poke() { }`, "for supports only the range form"},
		"three clause":    {`for i := 0; i < 3; i++ { poke() }`, "for supports only the range form"},
		"bare for":        {`for { poke() }`, "for supports only the range form"},
		"unterminated":    {`for i := range 3 { poke();`, "unterminated range body"},
		"return in body":  {`for i := range 3 { return i }`, "return cannot stand inside a range body"},
		"var in body":     {`for i := range 3 { var u url.URL }`, "var declaration cannot stand inside a range body"},
		"range over bool": {`for range true { poke() }`, "cannot range over this expression"},
		"range over expr": {`for range 1.5 { poke() }`, "cannot range over this expression"},
		"if in body":      {`for range xs { if poke() { } }`, "parse:"},
		// A single-quoted multi-rune string is a hand-grammar string
		// form; Go reads single quotes as a rune literal, so inside a
		// loop it stops parsing. The hand grammar keeps accepting it
		// outside loops: this is a surface cost of the delegation.
		"quoted body": {`for range xs { rec('ab') }`, "illegal rune literal"},
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

	// A body statement's parse error carries the offset of the
	// statement in the program, not in the synthetic wrapper: the
	// lowering re-enters the hand parser on the original source. The
	// standalone statement pins the parser's relative offset, and the
	// loop version must report it shifted by the statement's start.
	alone := (&Parser{}).mustFail(t, "x := y")
	src := "poke()\nfor i := range 3 { x := y }"
	inLoop := (&Parser{}).mustFail(t, src)
	wantSuffix := offsetSuffix(t, alone, strings.Index(src, "x := y"))
	if !strings.Contains(inLoop, "cannot assign a name to a name") || !strings.HasSuffix(inLoop, wantSuffix) {
		t.Fatalf("err = %q, want the hand parser's message ending %q", inLoop, wantSuffix)
	}
}

// BenchmarkParseLoop prices the delegated front end: one range
// statement with a two-statement body, scanned for its extent, parsed
// by go/parser inside the synthetic function, and lowered. The same
// source on a hand-rolled front end is the comparison the antithesis
// exists to measure; parsing happens once per Compile, never per run.
func BenchmarkParseLoop(b *testing.B) {
	const src = "c := counter();\nfor i := range 4 {\n\tc.Add(i);\n\tc.Add(1);\n}\nreturn c.Sum();"
	b.ReportAllocs()
	for b.Loop() {
		if _, err := (&Parser{}).Parse(src); err != nil {
			b.Fatal(err)
		}
	}
}

// mustFail parses src expecting an error and returns its message.
func (p *Parser) mustFail(t *testing.T, src string) string {
	t.Helper()
	_, err := p.Parse(src)
	if err == nil {
		t.Fatalf("parsed %q, want an error", src)
	}
	return err.Error()
}

// offsetSuffix takes a message ending in "offset N" and returns the
// same suffix with N shifted by delta.
func offsetSuffix(t *testing.T, msg string, delta int) string {
	t.Helper()
	i := strings.LastIndex(msg, "offset ")
	if i < 0 {
		t.Fatalf("no offset in %q", msg)
	}
	n, err := strconv.Atoi(msg[i+len("offset "):])
	if err != nil {
		t.Fatalf("bad offset in %q: %v", msg, err)
	}
	return fmt.Sprintf("offset %d", n+delta)
}
