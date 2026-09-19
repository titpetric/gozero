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

// TestTypeDeclParseEmbedded checks the embedded form: a type standing
// alone on its line, bare or dotted, with the field name taken from
// the base segment and an optional tag kept as written.
func TestTypeDeclParseEmbedded(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want fieldDecl
	}{
		"lone":      {"type P struct {\n\tBase\n}\nreturn f();", fieldDecl{name: "Base", typ: "Base", embedded: true}},
		"dotted":    {"type P struct {\n\thttp.Header\n}\nreturn f();", fieldDecl{name: "Header", typ: "http.Header", embedded: true}},
		"tagged":    {"type P struct {\n\tBase `json:\"b\"`\n}\nreturn f();", fieldDecl{name: "Base", typ: "Base", tag: `json:"b"`, embedded: true}},
		"semicolon": {"type P struct { Base; }\nreturn f();", fieldDecl{name: "Base", typ: "Base", embedded: true}},
	} {
		prog, err := (&Parser{}).Parse(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if f := prog.types[0].fields; len(f) != 1 || f[0] != tc.want {
			t.Errorf("%s: fields = %+v, want %+v", name, f, tc.want)
		}
	}

	// An embed shares its line with named fields like any field does.
	prog, err := (&Parser{}).Parse("type P struct { Base; M int64 }\nreturn f();")
	if err != nil {
		t.Fatal(err)
	}
	if f := prog.types[0].fields; len(f) != 2 || !f[0].embedded || f[1].embedded {
		t.Errorf("fields = %+v", f)
	}
}

// TestTypeDeclParseErrors pins the named rejections: every Go struct
// form the block does not admit errors by name instead of misparsing.
func TestTypeDeclParseErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"name list":        {"type P struct {\n\tX, Y int64\n}", "a field name list is not supported"},
		"embedded pointer": {"type P struct {\n\t*Base\n}", "an embedded pointer field is not supported"},
		"embedded half":    {"type P struct {\n\thttp.\n}", "expected a name after '.'"},
		"junk after embed": {"type P struct {\n\thttp.Header Y\n}", "expected ';' or end of line after field"},
		"single-quote":     {"type P struct {\n\tX int64 'json'\n}", "a struct tag is a raw or double-quoted string"},
		"unterminated tag": {"type P struct {\n\tX int64 `json:\"x\"\n}", "unterminated raw string"},
		"junk after tag":  {"type P struct {\n\tX int64 `t` Y\n}", "expected ';' or end of line after field"},
		"missing brace":   {"type P struct\nX int64", "expected '{' after struct"},
		"unterminated":    {"type P struct {\n\tX int64\n", "unterminated struct body"},
		"junk after type": {"type P struct {\n\tX int64 Y\n}", "expected ';' or end of line after field"},
		"junk after body": {"type P struct { X int64 } f()", "expected ';' or end of line"},
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
