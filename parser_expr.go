package gozero

import (
	"fmt"
)

// The expression grammar, precedence climbing over the argument
// atoms parser_arg.go always had:
//
//	rhs     := binexpr(1)
//	binexpr := unary { binop binexpr }     // while prec >= min, left-assoc
//	unary   := ( "+" | "-" | "!" | "^" ) unary | primary
//	primary := "(" binexpr(1) ")" | arg    // the original argument forms
//
// It applies in exactly one position: the right side of an assignment
// to a name. A binary operator never starts a line, because the end
// of a line closes a statement, so continuation is spelled with the
// operator trailing, as in Go. "&" and "*" stay at the atom level:
// "&" prefixes a composite literal and "*" has no deref form, so
// neither is claimed as a unary operator here.

// exprArg reads a full expression where an assignment's right side
// begins. The result is a plain arg when no operator followed the
// first atom, and an argBinary or argUnary tree otherwise.
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
			// "!=" at unary position starts nothing; letting the "!"
			// through would misread the "=" as an assignment inside a
			// value.
			if c == '!' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '=' {
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

// primary reads one atom: a parenthesized expression or an argument.
func (p *Parser) primary() (arg, error) {
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == '(' {
		p.pos++
		p.nl = false
		a, err := p.exprArg()
		if err != nil {
			return arg{}, err
		}
		if !p.consume(')') {
			return arg{}, fmt.Errorf("parse: expected ')' at offset %d", p.pos)
		}
		return a, nil
	}
	return p.arg()
}
