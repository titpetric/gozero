package gozero

import (
	"fmt"
)

// Conditions: if / else if / else over braced statement lists. The
// condition is deliberately not an expression: it is a name, a field
// path, or a call, and the compiler checks it is bool. There is no
// init clause and no operator; docs/design/conditions.md records what
// the fuller forms cost.
//
//	ifstmt := "if" cond block [ "else" ( ifstmt | block ) ]
//	cond   := path | expr
//	block  := "{" { stmt } "}"
//
// block is general on purpose: it reads the same statement list the
// top level does, so a later construct with a braced body reuses it.

// ifStmt is one if with its else chain. An else-if nests: els holds
// a single statement that is itself an if.
type ifStmt struct {
	cond arg
	then []stmt
	els  []stmt
}

// parseIf reads an if statement after the keyword.
func (p *Parser) parseIf() (stmt, error) {
	is := &ifStmt{}
	cond, err := p.cond()
	if err != nil {
		return stmt{}, err
	}
	is.cond = cond
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

// cond reads an if condition: a call or a dotted path. It is not
// p.arg on purpose: the header holds no literals, and a path followed
// by '{' must open the block rather than a composite literal, Go's
// own rule for an if header.
func (p *Parser) cond() (arg, error) {
	// A literal cannot open a condition: a digit or a quote here is
	// caught now, rather than surfacing as a strange unbound name.
	if c := p.peek(); c == '-' || c == '"' || c == '\'' || (c >= '0' && c <= '9') {
		return arg{}, fmt.Errorf("parse: a literal is not an if condition at offset %d", p.pos)
	}
	save, saveNL := p.pos, p.nl
	if call, err := p.expr(); err == nil {
		return arg{kind: argCall, sub: call}, nil
	}
	p.pos, p.nl = save, saveNL
	path, err := p.path()
	if err != nil {
		return arg{}, fmt.Errorf("parse: expected an if condition at offset %d", p.pos)
	}
	if len(path) == 1 {
		return arg{kind: argVar, str: path[0]}, nil
	}
	return arg{kind: argPath, path: path}, nil
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
