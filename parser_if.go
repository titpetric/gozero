package gozero

import (
	"fmt"
)

// Conditions: if / else if / else over braced statement lists. The
// condition is a bool name, a bool field path, a call returning bool,
// or one comparison between two operands; the operand set is closed
// and there is no init clause, no && and no nesting.
// docs/design/conditions.md records what the fuller forms cost.
//
//	ifstmt  := "if" cond block [ "else" ( ifstmt | block ) ]
//	cond    := operand [ cmpop operand ]
//	operand := path | expr | string | number
//	cmpop   := "==" | "!=" | "<" | "<=" | ">" | ">="
//	block   := "{" { stmt } "}"
//
// block is general on purpose: it reads the same statement list the
// top level does, so a later construct with a braced body reuses it,
// and the comparison kit it reads its header with is parser_cmp.go.

// ifStmt is one if with its else chain. An else-if nests: els holds
// a single statement that is itself an if. Exactly one of cond and
// cmp is set.
type ifStmt struct {
	cond arg
	cmp  *cmpExpr
	then []stmt
	els  []stmt
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
		if err := p.rejectBlockDecl(); err != nil {
			return nil, err
		}
		s, err := p.stmt()
		if err != nil {
			return nil, err
		}
		list = append(list, s)
	}
}
