package gozero

import (
	"fmt"
)

// The expression grammar, precedence climbing over the argument
// atoms parser_arg.go always had:
//
//	expr    := binexpr(1)
//	binexpr := unary { binop binexpr }     // while prec >= min, left-assoc
//	unary   := ( "+" | "-" | "!" | "^" ) unary | primary
//	primary := atom { "[" expr "]" }
//	atom    := "(" expr ")" | arg          // the original argument forms
//
// A binary operator never starts a line: the end of a line closes a
// statement, so continuation is spelled with the operator trailing,
// as in Go. "&" and "*" stay at the atom level: "&" prefixes a
// composite literal and "*" has no deref form yet, so neither is
// claimed as a unary operator here.

// exprArg reads a full expression where an argument is expected.
func (p *Parser) exprArg() (arg, error) {
	return p.binExpr(1)
}

func (p *Parser) binExpr(minPrec int) (arg, error) {
	left, err := p.unaryExpr()
	if err != nil {
		return arg{}, err
	}
	for {
		p.skipSpace()
		if p.nl {
			return left, nil
		}
		op, prec := p.binOp()
		if op == "" || prec < minPrec {
			return left, nil
		}
		p.pos += len(op)
		p.nl = false
		right, err := p.binExpr(prec + 1)
		if err != nil {
			return arg{}, err
		}
		l := left
		left = arg{kind: argBinary, op: op, x: &l, y: &right}
	}
}

func (p *Parser) unaryExpr() (arg, error) {
	p.skipSpace()
	if p.pos < len(p.src) {
		switch c := p.src[p.pos]; c {
		case '+', '-', '!', '^':
			// A minus straight onto a digit stays the negative literal
			// numberLit reads, so -1 is a constant, not an operation.
			if c == '-' && p.pos+1 < len(p.src) && p.src[p.pos+1] >= '0' && p.src[p.pos+1] <= '9' {
				break
			}
			p.pos++
			p.nl = false
			x, err := p.unaryExpr()
			if err != nil {
				return arg{}, err
			}
			if c == '+' {
				// Unary plus is the identity, as in Go.
				return x, nil
			}
			return arg{kind: argUnary, op: string(c), x: &x}, nil
		}
	}
	return p.primary()
}

// primary reads an atom and its index suffixes.
func (p *Parser) primary() (arg, error) {
	p.skipSpace()
	var a arg
	var err error
	if p.pos < len(p.src) && p.src[p.pos] == '(' {
		p.pos++
		p.nl = false
		saved := p.hdr
		p.hdr = false
		a, err = p.exprArg()
		p.hdr = saved
		if err != nil {
			return arg{}, err
		}
		if !p.consume(')') {
			return arg{}, p.errAt(p.pos, "expected ')'")
		}
	} else {
		a, err = p.arg()
		if err != nil {
			return arg{}, err
		}
	}
	for {
		p.skipSpace()
		// An opening bracket on a new line starts nothing, as under
		// Go's semicolon rule.
		if p.nl || p.pos >= len(p.src) || p.src[p.pos] != '[' {
			return a, nil
		}
		p.pos++
		p.nl = false
		idx, err := p.exprArg()
		if err != nil {
			return arg{}, err
		}
		if !p.consume(']') {
			return arg{}, p.errAt(p.pos, "expected ']'")
		}
		base := a
		a = arg{kind: argIndex, x: &base, y: &idx}
	}
}

func (p *Parser) expr() (*callExpr, error) {
	path, err := p.path()
	if err != nil {
		return nil, err
	}
	if !p.consume('(') {
		return nil, fmt.Errorf("parse: expected '(' at offset %d", p.pos)
	}
	args, err := p.args()
	if err != nil {
		return nil, err
	}
	call := &callExpr{path: path, args: args}

	for {
		save := p.pos
		p.skipSpace()
		if !p.consume('.') {
			p.pos = save
			return call, nil
		}
		name := p.ident()
		if name == "" {
			return nil, fmt.Errorf("parse: expected method name at offset %d", p.pos)
		}
		if !p.consume('(') {
			return nil, fmt.Errorf("parse: expected '(' at offset %d", p.pos)
		}
		largs, err := p.args()
		if err != nil {
			return nil, err
		}
		call.chain = append(call.chain, link{name: name, args: largs})
	}
}

func (p *Parser) path() ([]string, error) {
	name := p.ident()
	if name == "" {
		return nil, fmt.Errorf("parse: expected a name at offset %d", p.pos)
	}
	path := []string{name}
	for {
		save := p.pos
		if !p.consume('.') {
			return path, nil
		}
		next := p.ident()
		if next == "" {
			p.pos = save
			return path, nil
		}
		path = append(path, next)
	}
}
