package gozero

import (
	"fmt"
)

// Conditions, the L1 and L2 rungs: if / else if / else over braced
// statement lists. The condition is a bool name, a bool field path,
// a call returning bool, or one comparison between two operands; the
// operand set is closed and there is no init clause, no && and no
// nesting. docs/design/conditions.md records what the fuller forms
// cost.
//
//	ifstmt  := "if" cond block [ "else" ( ifstmt | block ) ]
//	cond    := operand [ cmpop operand ]
//	operand := path | expr | string | number
//	cmpop   := "==" | "!=" | "<" | "<=" | ">" | ">="
//	block   := "{" { stmt } "}"

// ifStmt is one if with its else chain. An else-if nests: els holds
// a single statement that is itself an if. Exactly one of cond and
// cmp is set.
type ifStmt struct {
	cond arg
	cmp  *cmpExpr
	then []stmt
	els  []stmt
}

// cmpExpr is a comparison in an if header: two operands and the
// operator between them. It exists only there; every other position
// rejects the operator by name (comparison placement).
type cmpExpr struct {
	op       string
	lhs, rhs arg
}

// parseIf reads an if statement after the keyword.
func (p *Parser) parseIf() (stmt, error) {
	is := &ifStmt{}
	cond, cmp, err := p.cond()
	if err != nil {
		return stmt{}, err
	}
	is.cond, is.cmp = cond, cmp
	if is.then, err = p.block(); err != nil {
		return stmt{}, err
	}

	// else binds only on the same line as the closing brace, the
	// gofmt shape of Go.
	save, saveNL := p.pos, p.nl
	p.skipSpace()
	if !p.nl && p.keyword("else") {
		if p.keyword("if") {
			sub, err := p.parseIf()
			if err != nil {
				return stmt{}, err
			}
			// The nested if consumed the statement's terminator.
			is.els = []stmt{sub}
			return stmt{ifs: is}, nil
		}
		if is.els, err = p.block(); err != nil {
			return stmt{}, err
		}
	} else {
		p.pos, p.nl = save, saveNL
	}
	if !p.terminated() {
		return stmt{}, fmt.Errorf("parse: expected ';' or end of line after if at offset %d", p.pos)
	}
	return stmt{ifs: is}, nil
}

// cond reads an if condition: one operand standing as a bool, or a
// comparison between two. A bare literal is caught here: it can only
// be a comparison's side, never the whole condition.
func (p *Parser) cond() (arg, *cmpExpr, error) {
	at := p.pos
	lhs, lit, err := p.condOperand()
	if err != nil {
		return arg{}, nil, err
	}
	if op, ok := p.cmpOp(); ok {
		rhs, _, err := p.condOperand()
		if err != nil {
			return arg{}, nil, err
		}
		return arg{}, &cmpExpr{op: op, lhs: lhs, rhs: rhs}, nil
	}
	if lit {
		return arg{}, nil, fmt.Errorf("parse: a literal is not an if condition at offset %d", at)
	}
	return lhs, nil, nil
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
	save := p.pos
	if call, err := p.expr(); err == nil {
		return arg{kind: argCall, sub: call}, false, nil
	}
	p.pos = save
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

// block reads a braced statement list.
func (p *Parser) block() ([]stmt, error) {
	if !p.consume('{') {
		return nil, fmt.Errorf("parse: expected '{' at offset %d", p.pos)
	}
	var list []stmt
	for {
		if p.consume('}') {
			return list, nil
		}
		p.skipSpace()
		if p.pos >= len(p.src) {
			return nil, fmt.Errorf("parse: unterminated block at offset %d", p.pos)
		}
		s, err := p.stmt()
		if err != nil {
			return nil, err
		}
		list = append(list, s)
	}
}
