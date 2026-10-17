package gozero

import (
	"testing"
)

// TestParseTypeRef checks the pointer and slice spellings a var
// statement reads, matching reflect.Type.String, and that the retired
// channel syntax fails to parse.
func TestParseTypeRef(t *testing.T) {
	for src, want := range map[string]string{
		`var u *url.URL;`:  "*url.URL",
		`var s []string;`:  "[]string",
		`var m [][]byte;`:  "[][]byte",
		`var ch chanLike;`: "chanLike",
	} {
		prog, err := (&Parser{}).Parse(src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if got := prog.stmts[0].varType; got != want {
			t.Errorf("%s: typeref = %q, want %q", src, got, want)
		}
	}

	for name, src := range map[string]string{
		"receive":   `v := <-c;`,
		"send":      `c <- "x";`,
		"chan var":  `var c chan string;`,
		"recv chan": `var c <-chan string;`,
		"send chan": `var c chan<- string;`,
	} {
		if _, err := (&Parser{}).Parse(src); err == nil {
			t.Errorf("%s: expected a parse error for %q", name, src)
		}
	}
}
