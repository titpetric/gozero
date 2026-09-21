package gozero

import (
	"fmt"
)

// The range loop, "for" over "range", with the braced statement list
// parser_if.go already reads. The condition and three-clause headers
// are parser_for.go, which is also what dispatches to this reader.
//
//	forstmt := "for" [ name [ "," name ] ":=" ] "range" arg block term
//	block   := "{" { stmt } "}"
//
// A body holds what a program holds, minus the two declaration forms
// and return; break and continue are the other way round, admitted
// only inside one. Both rules are the parser's, decided at the
// statement that breaks them, and depth is what the body reads them
// off.

// rangeStmt is a parsed range loop. key and val are the iteration
// names, "" when the form binds fewer than two and "_" when written
// blank; over is the ranged expression and body the braced statement
// list.
type rangeStmt struct {
	key  string
	val  string
	over arg
	body []stmt
}

// forRange reads a range loop, once forLoop has sniffed the range
// keyword in the header.
func (p *Parser) forRange() (stmt, error) {
	r := &rangeStmt{}
	if !p.keyword("range") {
		lhs, define, ok := p.assignList()
		if !ok {
			return stmt{}, fmt.Errorf("parse: for takes a range, a condition or three clauses at offset %d", p.pos)
		}
		if !define {
			return stmt{}, fmt.Errorf("parse: a range loop declares its names with := at offset %d", p.pos)
		}
		if len(lhs) > 2 {
			return stmt{}, fmt.Errorf("parse: a range loop binds at most two names at offset %d", p.pos)
		}
		if !p.keyword("range") {
			return stmt{}, fmt.Errorf("parse: for takes a range, a condition or three clauses at offset %d", p.pos)
		}
		r.key = lhs[0]
		if len(lhs) == 2 {
			r.val = lhs[1]
		}
	}

	// The header holds composite literals back, so the brace after the
	// ranged expression opens the body.
	saved := p.hdr
	p.hdr = true
	over, err := p.arg()
	p.hdr = saved
	if err != nil {
		return stmt{}, err
	}
	switch over.kind {
	case argVar, argPath, argCall, argInt:
	default:
		return stmt{}, fmt.Errorf("parse: cannot range over this expression at offset %d", p.pos)
	}
	r.over = over

	p.depth++
	r.body, err = p.block()
	p.depth--
	if err != nil {
		return stmt{}, err
	}
	if !p.terminated() {
		return stmt{}, fmt.Errorf("parse: expected ';' or end of line after the loop at offset %d", p.pos)
	}
	return stmt{rng: r}, nil
}

// loopExit reads the rest of a break or continue statement: nothing.
// Both must stand inside a loop body, both exit the innermost loop
// only, and a label is rejected by name rather than as a stray token.
func (p *Parser) loopExit(kw string, s stmt) (stmt, error) {
	if p.depth == 0 {
		return stmt{}, fmt.Errorf("parse: %s is only allowed inside a loop body (offset %d)", kw, p.pos)
	}
	if p.terminated() {
		return s, nil
	}
	if p.ident() != "" {
		return stmt{}, fmt.Errorf("parse: a label after %s is not in the language, %s exits the innermost loop (offset %d)", kw, kw, p.pos)
	}
	return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
}
