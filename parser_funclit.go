package gozero

import (
	"fmt"
)

// A func literal stands in argument position and nowhere else:
//
//	funclit := "func" "(" [ name { "," name } ] ")" block
//
// Parameters are names only. Their types come from the func signature
// of the parameter the literal fills, the way every other type in the
// language comes from a binding, so there is no type syntax to parse.
// The body is the braced statement list every other construct with a
// body reads, parser_if.go's block.

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
	p.skipSpace()
	if p.pos >= len(p.src) || p.src[p.pos] != '{' {
		return arg{}, fmt.Errorf("parse: expected '{' to open the func literal body at offset %d", p.pos)
	}

	// The body is a program of its own, so the loop depth resets
	// across the boundary: a var and a return stand in a body the way
	// they stand at the top level, and break and continue do not,
	// even when the literal is written inside a loop.
	depth := p.depth
	p.depth = 0
	list, err := p.block()
	p.depth = depth
	if err != nil {
		return arg{}, err
	}
	fl.body = &program{stmts: list}
	return arg{kind: argFuncLit, fn: fl}, nil
}
