package gozero

// The operator scanner for the expression grammar. binOp peeks
// without consuming: the caller decides on precedence and newline
// rules, then consumes with p.pos += len(op) and clears p.nl.
//
// Go's binary precedence, 5 binding tightest:
//
//	5: *  /  %  <<  >>  &  &^
//	4: +  -  |  ^
//	3: ==  !=  <  <=  >  >=
//	2: &&
//	1: ||

// binOp reports the binary operator at the current position and its
// precedence, or "" when the next bytes are not one. Two-byte
// operators are matched before their one-byte prefixes (maximal
// munch), "<-" is never binary (it is a receive or a send, and a < -b
// needs the space, as in Go), "++" and "--" are steps, and "//" opens
// a comment, not a division.
func (p *Parser) binOp() (string, int) {
	if p.pos >= len(p.src) {
		return "", 0
	}
	c := p.src[p.pos]
	var d byte
	if p.pos+1 < len(p.src) {
		d = p.src[p.pos+1]
	}
	switch c {
	case '<':
		switch d {
		case '-':
			return "", 0
		case '<':
			return "<<", 5
		case '=':
			return "<=", 3
		}
		return "<", 3
	case '>':
		switch d {
		case '>':
			return ">>", 5
		case '=':
			return ">=", 3
		}
		return ">", 3
	case '=':
		if d == '=' {
			return "==", 3
		}
		return "", 0
	case '!':
		if d == '=' {
			return "!=", 3
		}
		return "", 0
	case '&':
		switch d {
		case '&':
			return "&&", 2
		case '^':
			return "&^", 5
		}
		return "&", 5
	case '|':
		if d == '|' {
			return "||", 1
		}
		return "|", 4
	case '/':
		if d == '/' {
			return "", 0
		}
		return "/", 5
	case '*':
		return "*", 5
	case '%':
		return "%", 5
	case '+':
		if d == '+' {
			return "", 0
		}
		return "+", 4
	case '-':
		if d == '-' {
			return "", 0
		}
		return "-", 4
	case '^':
		return "^", 4
	}
	return "", 0
}
