package gozero

import (
	"testing"
)

// TestBinOpScanner pins the operator scanner: every spelling, its
// precedence level, maximal munch on the two-byte forms, and the
// byte pairs that are not binary operators at all.
func TestBinOpScanner(t *testing.T) {
	for src, want := range map[string]struct {
		op   string
		prec int
	}{
		"*x":  {"*", 5},
		"/x":  {"/", 5},
		"%x":  {"%", 5},
		"<<x": {"<<", 5},
		">>x": {">>", 5},
		"&x":  {"&", 5},
		"&^x": {"&^", 5},
		"+x":  {"+", 4},
		"-x":  {"-", 4},
		"|x":  {"|", 4},
		"^x":  {"^", 4},
		"==x": {"==", 3},
		"!=x": {"!=", 3},
		"<x":  {"<", 3},
		"<=x": {"<=", 3},
		">x":  {">", 3},
		">=x": {">=", 3},
		"&&x": {"&&", 2},
		"||x": {"||", 1},

		// Not operators: receive, steps, assignment, comment, negation
		// without a right-hand comparison.
		"<-x": {"", 0},
		"++x": {"", 0},
		"--x": {"", 0},
		"=x":  {"", 0},
		"//x": {"", 0},
		"!x":  {"", 0},
		"x":   {"", 0},
		"":    {"", 0},
	} {
		p := &Parser{src: src}
		op, prec := p.binOp()
		if op != want.op || prec != want.prec {
			t.Errorf("%q: scanned %q at %d, want %q at %d", src, op, prec, want.op, want.prec)
		}
		if p.pos != 0 {
			t.Errorf("%q: the scanner consumed input", src)
		}
	}
}

// TestPeekBinOp covers the rejection sniff the other value positions
// use: it sees an operator on the same line and nothing across a
// newline.
func TestPeekBinOp(t *testing.T) {
	p := &Parser{src: "  + x"}
	if op := p.peekBinOp(); op != "+" {
		t.Errorf("peeked %q, want +", op)
	}
	if p.pos != 0 {
		t.Error("peek consumed input")
	}
	p = &Parser{src: "\n+ x"}
	if op := p.peekBinOp(); op != "" {
		t.Errorf("peeked %q across a newline, want nothing", op)
	}
}
