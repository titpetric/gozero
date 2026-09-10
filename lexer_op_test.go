package gozero

import (
	"testing"
)

// TestBinOpScan covers the operator peek: every operator at its
// precedence, maximal munch, and the refusals that keep receive,
// comments and assignment out of the expression grammar.
func TestBinOpScan(t *testing.T) {
	for src, want := range map[string]struct {
		op   string
		prec int
	}{
		"* x": {"*", 5}, "/ x": {"/", 5}, "% x": {"%", 5},
		"<< x": {"<<", 5}, ">> x": {">>", 5}, "& x": {"&", 5}, "&^ x": {"&^", 5},
		"+ x": {"+", 4}, "- x": {"-", 4}, "| x": {"|", 4}, "^ x": {"^", 4},
		"== x": {"==", 3}, "!= x": {"!=", 3},
		"< x": {"<", 3}, "<= x": {"<=", 3}, "> x": {">", 3}, ">= x": {">=", 3},
		"&& x": {"&&", 2},
		"|| x": {"||", 1},
		"<- x": {"", 0}, // receive, never binary
		"// c": {"", 0}, // comment, not division
		"/* c": {"", 0},
		"= x":  {"", 0}, // assignment
		"! x":  {"", 0}, // unary only
	} {
		p := &Parser{src: src}
		op, prec := p.binOp()
		if op != want.op || prec != want.prec {
			t.Errorf("%q: got %q %d, want %q %d", src, op, prec, want.op, want.prec)
		}
		if p.pos != 0 {
			t.Errorf("%q: binOp consumed input", src)
		}
	}
}

// TestUnaryOpScan covers the unary peek, including Go's no-op unary
// plus and the receive refusal.
func TestUnaryOpScan(t *testing.T) {
	for src, want := range map[string]string{
		"+x": "+", "-x": "-", "!ok": "!", "^a": "^", "&x": "&", "*p": "*",
		"<-c": "", "x": "",
	} {
		p := &Parser{src: src}
		op, ok := p.unaryOp()
		if op != want || ok != (want != "") {
			t.Errorf("%q: got %q %v, want %q", src, op, ok, want)
		}
		if p.pos != 0 {
			t.Errorf("%q: unaryOp consumed input", src)
		}
	}
}
