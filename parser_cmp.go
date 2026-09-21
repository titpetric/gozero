package gozero

import (
	"fmt"
)

// Comparisons: ==, !=, <, <=, > and >= between two operands. The
// operators exist in one place, a condition header, and nowhere
// else; every statement position that could swallow one rejects it
// by name (comparison placement).
//
//	cond    := operand [ cmpop operand ]
//	operand := path | expr | string | number
//	cmpop   := "==" | "!=" | "<" | "<=" | ">" | ">="
//
// The three pieces below are the header-comparison kit: the node a
// header hands the compiler, the operand reader, and the operator
// reader. They are deliberately not in parser_if.go, because a
// header is not only an if's.

// cmpExpr is a comparison in a condition header: two operands and
// the operator between them.
type cmpExpr struct {
	op       string
	lhs, rhs arg
}

// condOperand reads one condition operand: a literal, a call, or a
// dotted path. It is not p.arg on purpose: a path followed by '{'
// must open the block rather than a composite literal, Go's own rule
// for an if header. lit reports a literal, which cannot stand alone
// as the condition.
func (p *Parser) condOperand() (a arg, lit bool, err error) {
	if c := p.peek(); c == '-' || c == '"' || c == '\'' || (c >= '0' && c <= '9') {
		a, err = p.arg()
		return a, true, err
	}
	save, saveNL := p.pos, p.nl
	if call, err := p.expr(); err == nil {
		return arg{kind: argCall, sub: call}, false, nil
	}
	p.pos, p.nl = save, saveNL
	path, err := p.path()
	if err != nil {
		return arg{}, false, fmt.Errorf("parse: expected an if condition at offset %d", p.pos)
	}
	if len(path) == 1 {
		return arg{kind: argVar, str: path[0]}, false, nil
	}
	return arg{kind: argPath, path: path}, false, nil
}

// cmpOp reads a comparison operator, longest spelling first. '<'
// immediately followed by '-' is the channel arrow, never a
// comparison, matching Go's own tokenizer.
func (p *Parser) cmpOp() (string, bool) {
	if p.consumeStr("==") {
		return "==", true
	}
	if p.consumeStr("!=") {
		return "!=", true
	}
	if p.consumeStr("<=") {
		return "<=", true
	}
	if p.consumeStr(">=") {
		return ">=", true
	}
	p.skipSpace()
	if p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '<':
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == '-' {
				return "", false
			}
			p.pos++
			p.nl = false
			return "<", true
		case '>':
			p.pos++
			p.nl = false
			return ">", true
		}
	}
	return "", false
}

// rejectCmp fails with the placement rule when a comparison operator
// follows, which is how "y := x == 5" names its error instead of
// surfacing as a strange assignment.
func (p *Parser) rejectCmp() error {
	save, saveNL := p.pos, p.nl
	if _, ok := p.cmpOp(); ok {
		return fmt.Errorf("parse: a comparison is only legal in an if header (comparison placement) at offset %d", save)
	}
	p.pos, p.nl = save, saveNL
	return nil
}
