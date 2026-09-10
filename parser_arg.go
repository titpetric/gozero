package gozero

import (
	"fmt"
)

// The argument grammar: everything that can stand on the right of an
// assignment or inside a call's parentheses, plus the type reference
// a var statement reads. The statement level lives in parser.go.

// typeRef reads a type as written in a var statement: a dotted name,
// with any number of pointer and slice prefixes. The spelling matches
// reflect.Type.String(), which is what the registry is keyed by.
func (p *Parser) typeRef() (string, error) {
	prefix := ""
	for {
		p.skipSpace()
		if p.pos < len(p.src) && p.src[p.pos] == '*' {
			p.pos++
			prefix += "*"
			continue
		}
		if p.pos+1 < len(p.src) && p.src[p.pos] == '[' && p.src[p.pos+1] == ']' {
			p.pos += 2
			prefix += "[]"
			continue
		}
		// Channel prefixes spell what reflect.Type.String produces,
		// which is what the registry is keyed by: "chan T",
		// "<-chan T", "chan<- T".
		if p.consumeStr("<-") {
			if !p.keyword("chan") {
				return "", fmt.Errorf("parse: expected chan after <- at offset %d", p.pos)
			}
			prefix += "<-chan "
			continue
		}
		if p.keyword("chan") {
			if p.consumeStr("<-") {
				prefix += "chan<- "
			} else {
				prefix += "chan "
			}
			continue
		}
		break
	}
	path, err := p.path()
	if err != nil {
		return "", fmt.Errorf("parse: expected a type name at offset %d", p.pos)
	}
	return prefix + joinPath(path), nil
}

// args reads the argument list up to and including the closing paren.
func (p *Parser) args() ([]arg, error) {
	var out []arg
	for {
		p.skipSpace()
		if p.consume(')') {
			return out, nil
		}
		if len(out) > 0 && !p.consume(',') {
			return nil, fmt.Errorf("parse: expected ',' or ')' at offset %d", p.pos)
		}
		a, err := p.arg()
		if err != nil {
			return nil, err
		}
		if p.consumeStr("...") {
			a.spread = true
		}
		out = append(out, a)
	}
}

func (p *Parser) arg() (arg, error) {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return arg{}, fmt.Errorf("parse: unexpected end of input")
	}
	c := p.src[p.pos]
	switch {
	case c == '"' || c == '\'':
		return p.stringLit(c)
	case c == '<' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '-':
		p.pos += 2
		p.nl = false
		src, err := p.arg()
		if err != nil {
			return arg{}, err
		}
		switch src.kind {
		case argVar, argPath, argCall:
		default:
			return arg{}, fmt.Errorf("parse: expected a channel after <- at offset %d", p.pos)
		}
		return arg{kind: argRecv, recv: &src}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		return p.numberLit()
	case c == '&':
		// & only prefixes a composite literal: there are no other
		// addressable expressions in the grammar.
		p.pos++
		path, err := p.path()
		if err != nil {
			return arg{}, err
		}
		if !p.consume('{') {
			return arg{}, fmt.Errorf("parse: expected a composite literal after '&' at offset %d", p.pos)
		}
		return p.composite(path, true)
	default:
		save := p.pos
		path, err := p.path()
		if err != nil {
			return arg{}, err
		}
		if p.peek() == '(' {
			p.pos = save
			sub, err := p.expr()
			if err != nil {
				return arg{}, err
			}
			return arg{kind: argCall, sub: sub}, nil
		}
		if p.peek() == '{' {
			p.consume('{')
			return p.composite(path, false)
		}
		if len(path) != 1 {
			return arg{kind: argPath, path: path}, nil
		}
		// Keywords, not names. Before this they parsed as variable
		// references, missed the stack, and zero-filled: wantBool(true)
		// compiled and handed the callee false.
		switch path[0] {
		case "true":
			return arg{kind: argBool, b: true}, nil
		case "false":
			return arg{kind: argBool}, nil
		case "nil":
			return arg{kind: argNil}, nil
		}
		return arg{kind: argVar, str: path[0]}, nil
	}
}

// composite reads the elements of a composite literal after the
// opening brace. An element is "Field: value" or a bare value; the
// compiler checks the two forms are not mixed, because only it can
// name the fields. A trailing comma before the closing brace is
// legal, as it is in Go.
func (p *Parser) composite(path []string, addr bool) (arg, error) {
	a := arg{kind: argStruct, path: path, addr: addr}
	for {
		p.skipSpace()
		if p.consume('}') {
			return a, nil
		}
		if len(a.elems) > 0 && !p.consume(',') {
			return arg{}, fmt.Errorf("parse: expected ',' or '}' at offset %d", p.pos)
		}
		if p.consume('}') {
			return a, nil
		}
		var e structElem
		save := p.pos
		if name := p.ident(); name != "" && p.consume(':') {
			e.name = name
		} else {
			p.pos = save
		}
		v, err := p.arg()
		if err != nil {
			return arg{}, err
		}
		e.val = v
		a.elems = append(a.elems, e)
	}
}
