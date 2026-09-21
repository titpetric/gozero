package gozero

import (
	"strings"
	"testing"
)

// TestTypeDeclParse checks the block form: brace tracking, one field
// per line or per semicolon, and the declaration living alongside
// statements without disturbing them.
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

	// The compound spellings keep the reflect form the registry is
	// keyed by.
	prog, err = (&Parser{}).Parse("type B struct {\n\tP *url.URL\n\tR <-chan int64\n}\nreturn f();")
	if err != nil {
		t.Fatal(err)
	}
	if f := prog.types[0].fields; f[0].typ != "*url.URL" || f[1].typ != "<-chan int64" {
		t.Errorf("fields = %+v", f)
	}
}

// TestTypeDeclParseTags checks the tag token: raw and double-quoted
// spellings, the value kept exactly as written, and a tagless field
// staying empty.
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
	// Go; the tag is whatever the backquotes held.
	prog, err = (&Parser{}).Parse("type P struct {\n\tX int64 `a\r\nb`\n}\nreturn f();")
	if err != nil {
		t.Fatal(err)
	}
	if got := prog.types[0].fields[0].tag; got != "a\nb" {
		t.Errorf("tag = %q, want %q", got, "a\nb")
	}

	// A backquote on the next line is not this field's tag: a tag sits
	// on the field's own line, as Go's semicolon insertion demands.
	if _, err := (&Parser{}).Parse("type P struct {\n\tX int64\n\t`t`\n}\nreturn f();"); err == nil {
		t.Error("a next-line tag parsed; it should not attach to the field")
	}
}

// TestTypeDeclParseErrors pins the named rejections: every Go struct
// form the block does not admit errors by name instead of misparsing.
func TestTypeDeclParseErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"name list":       {"type P struct {\n\tX, Y int64\n}", "a field name list is not supported"},
		"embedded dotted": {"type P struct {\n\thttp.Header\n}", "an embedded field is not supported"},
		"embedded lone":   {"type P struct {\n\tBase\n}", "an embedded field is not supported"},
		"embedded tagged": {"type P struct {\n\tBase `json:\"b\"`\n}", "an embedded field is not supported"},
		"single-quote":    {"type P struct {\n\tX int64 'json'\n}", "a struct tag is a raw or double-quoted string"},
		"unterminated tag": {"type P struct {\n\tX int64 `json:\"x\"\n}", "unterminated raw string"},
		"junk after tag":  {"type P struct {\n\tX int64 `t` Y\n}", "expected ';' or end of line after field"},
		"missing brace":   {"type P struct\nX int64", "expected '{' after struct"},
		"unterminated":    {"type P struct {\n\tX int64\n", "unterminated struct body"},
		"junk after type": {"type P struct {\n\tX int64 Y\n}", "expected ';' or end of line after field"},
		"junk after body": {"type P struct { X int64 } f()", "expected ';' or end of line"},
		"nothing to run":  {"type P struct { X int64 }\n", "empty program"},
	} {
		_, err := (&Parser{}).Parse(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

// TestTypeDeclInBody pins the placement rule the chain's bodies make
// reachable: the sniff runs at the program level, so a declaration
// inside an if arm or a loop body is named rather than read as a call
// with a missing '('.
func TestTypeDeclInBody(t *testing.T) {
	for name, src := range map[string]string{
		"if arm":     "if ok { type P struct { X int64 }\n}\nreturn f();",
		"range body": "for x := range xs { type P struct { X int64 }\n}\nreturn f();",
		"for body":   "for ok { type P struct { X int64 }\n}\nreturn f();",
	} {
		_, err := (&Parser{}).Parse(src)
		if err == nil || !strings.Contains(err.Error(), "cannot stand inside a body") {
			t.Errorf("%s: err = %v, want the placement rule", name, err)
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
