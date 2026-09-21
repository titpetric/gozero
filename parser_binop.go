package gozero

import (
	"fmt"
)

// The operator assignment, "s := a + b". The right side of an
// assignment may combine exactly two values with +, == or !=, and
// that pair is the whole expression grammar: a second operator and
// parentheses are rejected by rule, so no expression tree exists for
// later features to inherit.
//
//	rhs   := binop | expr | string | number | ...
//	binop := arg ( "+" | "==" | "!=" ) arg
//
// Every other value position stays operator-free and says so by
// name, and the four ordering comparisons keep the placement rule
// they landed with: they read in an if or a three-clause for header
// and nowhere else.

// binopStmt finishes an assignment whose right side combines two
// values with a binary operator. Exactly one operator and no
// parentheses; the operands parse as any arg, and the compiler
// narrows them to a bound name or a literal.
func (p *Parser) binopStmt(lhs []string, define bool, x arg, op string) (stmt, error) {
	switch op {
	case "+", "==", "!=":
	default:
		return stmt{}, binopReject(op)
	}
	p.skipSpace()
	p.pos += len(op)
	p.nl = false
	if p.peek() == '(' {
		return stmt{}, fmt.Errorf("parse: parentheses do not group a value, one operator per assignment")
	}
	y, err := p.arg()
	if err != nil {
		return stmt{}, err
	}
	if next := p.peekBinOp(); next != "" {
		return stmt{}, fmt.Errorf("parse: one operator per assignment, bind the %s result to a name before %s", op, next)
	}
	if !p.terminated() {
		return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
	}
	return stmt{lhs: lhs, define: define, binOp: op, binX: &x, binY: &y}, nil
}

// binopReject names why an operator the assignment grammar does not
// take cannot stand where it does. The ordering four are a header's
// and keep the placement rule; every other spelling is not in the
// grammar at all.
func binopReject(op string) error {
	switch op {
	case "<", "<=", ">", ">=":
		return fmt.Errorf("parse: a comparison with %s is only legal in an if or for header (comparison placement)", op)
	}
	return fmt.Errorf("parse: operator %s is not in the grammar, an assignment combines two values with +, == or !=", op)
}

// rejectBareOperator names the rule for a statement that is only an
// operator expression, "x == 5;", before the call parse turns it
// into "expected '('". The sniff scans the path with ident and
// consume so a call statement pays no allocation for it, and it
// rewinds either way: the caller reads the same bytes as a call.
func (p *Parser) rejectBareOperator() error {
	save, saveNL := p.pos, p.nl
	defer func() { p.pos, p.nl = save, saveNL }()
	if p.ident() == "" {
		return nil
	}
	for {
		dot := p.pos
		if !p.consume('.') || p.ident() == "" {
			p.pos = dot
			break
		}
	}
	return p.rejectOperator("a statement")
}

// rejectOperator fails with the rule when an operator follows in a
// position the grammar keeps operator-free. where names the position
// as the message reads it: "returned", "an argument". The three
// operators an assignment takes point at the assignment; the rest
// report what they are.
func (p *Parser) rejectOperator(where string) error {
	switch op := p.peekBinOp(); op {
	case "":
		return nil
	case "+", "==", "!=":
		return fmt.Errorf("parse: an operator expression cannot be %s, assign it to a name first", where)
	default:
		return binopReject(op)
	}
}
