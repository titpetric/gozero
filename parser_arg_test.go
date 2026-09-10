package gozero

import (
	"testing"
)

// TestParseRecvArg checks the receive forms the argument grammar
// accepts and the sources it rejects.
func TestParseRecvArg(t *testing.T) {
	for _, src := range []string{
		`v := <-c;`,
		`v = <-c;`,
		`<-c;`,
		`v := <-b.C;`,
		`v := <-source();`,
	} {
		if _, err := (&Parser{}).Parse(src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	if _, err := (&Parser{}).Parse(`v := <-5;`); err == nil {
		t.Error("expected a parse error for a receive from a literal")
	}
}

// TestParseSendStmt checks the send statement and that the sniff does
// not misread assignments or calls.
func TestParseSendStmt(t *testing.T) {
	for src, want := range map[string]bool{
		`c <- "x";`:      true,
		`b.C <- "x";`:    true,
		`c <- source();`: true,
		`c <- 5;`:        true,
	} {
		prog, err := (&Parser{}).Parse(src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if got := prog.stmts[0].sendCh != nil; got != want {
			t.Errorf("%s: parsed as send = %v, want %v", src, got, want)
		}
	}
}

// TestParseChanTypeRef checks the channel spellings a var statement
// reads, matching reflect.Type.String.
func TestParseChanTypeRef(t *testing.T) {
	for src, want := range map[string]string{
		`var c chan string;`:    "chan string",
		`var c <-chan string;`:  "<-chan string",
		`var c chan<- string;`:  "chan<- string",
		`var c []chan int;`:     "[]chan int",
		`var c chan *url.URL;`:  "chan *url.URL",
		`var c chan chan int;`:  "chan chan int",
		`var ch chanLike;`:      "chanLike",
		`var c *chan struct;`:   "*chan struct",
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
}
