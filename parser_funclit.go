package gozero

import (
	"fmt"
)

// A func literal stands in argument position and nowhere else:
//
//	funclit := "func" "(" [ name { "," name } ] ")" "{" { stmt } "}"
//
// Parameters are names only. Their types come from the func signature
// of the parameter the literal fills, the way every other type in the
// language comes from a binding, so there is no type syntax to parse.
// The body is the same straight-line statement list a program is.

// funcLit is one parsed func literal: the parameter names and the
// body. pos is the offset of the func keyword, for diagnostics.
type funcLit struct {
	params []string
	body   *program
	pos    int
}

// funcLit reads a literal from just after the func keyword; the caller
// has already seen the '(' that starts the parameter list. pos is
// where the keyword began.
func (p *Parser) funcLit(pos int) (arg, error) {
	fl := &funcLit{pos: pos}
	p.consume('(')
	for {
		p.skipSpace()
		if p.consume(')') {
			break
		}
		if len(fl.params) > 0 && !p.consume(',') {
			return arg{}, fmt.Errorf("parse: expected ',' or ')' in the parameter list at offset %d", p.pos)
		}
		name := p.ident()
		if name == "" {
			return arg{}, fmt.Errorf("parse: expected a parameter name at offset %d; parameter types come from the target signature and are not written", p.pos)
		}
		fl.params = append(fl.params, name)
	}
	if !p.consume('{') {
		return arg{}, fmt.Errorf("parse: expected '{' to open the func literal body at offset %d", p.pos)
	}

	// The body shares the statement grammar. Inside it the closing
	// brace ends a statement like a semicolon does, which p.depth
	// arms in terminated.
	p.depth++
	defer func() { p.depth-- }()
	body := &program{}
	for {
		p.skipSpace()
		if p.consume('}') {
			break
		}
		if p.pos >= len(p.src) {
			return arg{}, fmt.Errorf("parse: unterminated func literal body at offset %d", p.pos)
		}
		s, err := p.stmt()
		if err != nil {
			return arg{}, err
		}
		body.stmts = append(body.stmts, s)
	}
	fl.body = body
	return arg{kind: argFuncLit, fn: fl}, nil
}
