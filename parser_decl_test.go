package gozero

import (
	"strings"
	"testing"
)

// TestTypeDeclParse checks the go/parser front end: fields come off
// the AST, the declaration lives alongside statements without
// disturbing them, and the compound spellings keep the reflect form
// the registry is keyed by.
func TestTypeDeclParse(t *testing.T) {
	src := "type Point struct {\n\tX int64\n\tY float64\n}\np := f();\n"
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.types) != 1 || len(prog.stmts) != 1 {
		t.Fatalf("types = %d, stmts = %d, want 1 and 1", len(prog.types), len(prog.stmts))
	}
	td := prog.types[0]
	if td.name != "Point" || len(td.fields) != 2 {
		t.Fatalf("decl = %+v", td)
	}
	if td.fields[0] != (fieldDecl{name: "X", typ: "int64"}) || td.fields[1] != (fieldDecl{name: "Y", typ: "float64"}) {
		t.Errorf("fields = %+v", td.fields)
	}

	for name, tc := range map[string]struct {
		src    string
		fields int
	}{
		"single line":       {"type B struct { N int64 }\nreturn f();", 1},
		"semicolon fields":  {"type B struct { N int64; M string }\nreturn f();", 2},
		"empty struct":      {"type B struct {\n}\nreturn f();", 0},
		"compound typerefs": {"type B struct {\n\tP *url.URL\n\tD []byte\n\tC chan string\n}\nreturn f();", 3},
		"after a statement": {"x := f();\ntype B struct { N int64 }\nreturn g(x);", 1},
		// The forms below are what the stdlib front end admits that the
		// hand-rolled block parser rejected by name.
		"name list":       {"type B struct {\n\tX, Y int64\n}\nreturn f();", 2},
		"comment in body": {"type B struct {\n\t// N counts.\n\tN int64\n}\nreturn f();", 1},
		"brace in string": {"type B struct {\n\tN int64 `k:\"{\"`\n}\nreturn f();", 1},
	} {
		prog, err := (&Parser{}).Parse(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(prog.types) != 1 || len(prog.types[0].fields) != tc.fields {
			t.Errorf("%s: types = %+v", name, prog.types)
		}
	}

	// A name list is sugar for one field per name: both carry the
	// shared type and tag.
	prog, err = (&Parser{}).Parse("type B struct {\n\tX, Y int64 `json:\"-\"`\n}\nreturn f();")
	if err != nil {
		t.Fatal(err)
	}
	want := []fieldDecl{{name: "X", typ: "int64", tag: `json:"-"`}, {name: "Y", typ: "int64", tag: `json:"-"`}}
	if f := prog.types[0].fields; len(f) != 2 || f[0] != want[0] || f[1] != want[1] {
		t.Errorf("fields = %+v", f)
	}

	// The compound spellings keep the reflect form the registry is
	// keyed by; types.ExprString prints them the way reflect does.
	prog, err = (&Parser{}).Parse("type B struct {\n\tP *url.URL\n\tR <-chan int64\n}\nreturn f();")
	if err != nil {
		t.Fatal(err)
	}
	if f := prog.types[0].fields; f[0].typ != "*url.URL" || f[1].typ != "<-chan int64" {
		t.Errorf("fields = %+v", f)
	}
}

// TestTypeDeclParseTags checks the tag comes off the AST unquoted:
// raw and double-quoted spellings, the value kept as written, and a
// tagless field staying empty.
func TestTypeDeclParseTags(t *testing.T) {
	src := "type Reply struct {\n" +
		"\tStatus string `json:\"status\"`\n" +
		"\tCount int64 `json:\"count,omitempty\" xml:\"count\"`\n" +
		"\tQuoted string \"json:'q'\"\n" +
		"\tBare int64\n" +
		"}\nreturn f();"
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []fieldDecl{
		{name: "Status", typ: "string", tag: `json:"status"`},
		{name: "Count", typ: "int64", tag: `json:"count,omitempty" xml:"count"`},
		{name: "Quoted", typ: "string", tag: "json:'q'"},
		{name: "Bare", typ: "int64"},
	}
	fields := prog.types[0].fields
	if len(fields) != len(want) {
		t.Fatalf("fields = %+v", fields)
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Errorf("field %d = %+v, want %+v", i, fields[i], want[i])
		}
	}

	// A raw string spans lines with carriage returns discarded, as in
	// Go; strconv.Unquote applies the same rule go/scanner does.
	prog, err = (&Parser{}).Parse("type P struct {\n\tX int64 `a\r\nb`\n}\nreturn f();")
	if err != nil {
		t.Fatal(err)
	}
	if got := prog.types[0].fields[0].tag; got != "a\nb" {
		t.Errorf("tag = %q, want %q", got, "a\nb")
	}

	// A backquote on the next line is not this field's tag: Go's
	// semicolon insertion ends the field, and go/parser rejects the
	// stray literal where the hand parser needed its own rule.
	if _, err := (&Parser{}).Parse("type P struct {\n\tX int64\n\t`t`\n}\nreturn f();"); err == nil {
		t.Error("a next-line tag parsed; it should not attach to the field")
	}
}

// TestTypeDeclParseErrors pins the named rejections. The semantic
// cuts keep their own messages; the grammatical ones are go/parser's
// and go/scanner's words, because those passes own the grammar now.
func TestTypeDeclParseErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"embedded dotted":  {"type P struct {\n\thttp.Header\n}", "an embedded field is not supported"},
		"embedded lone":    {"type P struct {\n\tBase\n}", "an embedded field is not supported"},
		"embedded tagged":  {"type P struct {\n\tBase `json:\"b\"`\n}", "an embedded field is not supported"},
		"inline struct":    {"type P struct {\n\tX struct{ Y int64 }\n}", "an inline struct field is not supported"},
		"single-quote":     {"type P struct {\n\tX int64 'json'\n}", "rune literal"},
		"unterminated tag": {"type P struct {\n\tX int64 `json:\"x\"\n}", "not terminated"},
		"missing brace":    {"type P struct\nX int64", "expected '{' after struct"},
		"unterminated":     {"type P struct {\n\tX int64\n", "unterminated struct body"},
		"junk after type":  {"type P struct {\n\tX int64 Y\n}", "expected"},
		"junk after body":  {"type P struct { X int64 } f()", "expected ';' or end of line"},
	} {
		_, err := (&Parser{}).Parse(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

// TestTypeDeclSniffRewind checks the sniff consumes nothing when the
// source only starts like a declaration, so a name beginning with
// those letters keeps parsing as it did.
func TestTypeDeclSniffRewind(t *testing.T) {
	for name, src := range map[string]string{
		"assignment":  "typed := f();",
		"lone ident":  "type := f();",
		"not struct":  "type x structural();", // parses as a call path
		"struct call": "structs := f();",
	} {
		prog, err := (&Parser{}).Parse(src)
		if name == "not struct" {
			// "type x structural()" is not a statement the grammar has;
			// what matters is the error is the statement parser's, not
			// the declaration's.
			if err == nil || strings.Contains(err.Error(), "struct") {
				t.Errorf("%s: err = %v", name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(prog.types) != 0 || len(prog.stmts) != 1 {
			t.Errorf("%s: types = %d, stmts = %d", name, len(prog.types), len(prog.stmts))
		}
	}
}
