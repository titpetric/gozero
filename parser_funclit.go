package gozero

import (
	"errors"
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/scanner"
	"go/token"
)

// A func literal stands in argument position and nowhere else. Its
// text is Go, so the stdlib parses it: go/scanner finds the extent of
// the literal inside the surrounding program, go/parser turns exactly
// that slice into an *ast.FuncLit, and the lowering in
// parser_funclit_lower.go walks the ast back into the parser's own
// statement structures. Parameters are
// names only, spelled func(w, r); go/parser reads them as unnamed
// parameters whose types are w and r, and the lowering takes those
// idents as the names, because the types come from the func signature
// of the parameter the literal fills, the way every other type in the
// language comes from a binding.
//
// go/parser owns the literal's syntax. Go spellings the byte-level
// grammar does not know work inside a body for free: raw strings,
// hex and underscored numbers, parentheses around a value, a trailing
// comma in the parameter list. The same coin's other face: gozero's
// single-quoted strings are Go rune literals, so 'ok' is rejected
// inside a body with go/parser's own message, and Go statements the
// language declines (if, for, go, defer) parse and are rejected by
// name in the lowering.

// funcLit is one parsed func literal: the parameter names and the
// body. pos is the offset of the func keyword, for diagnostics.
type funcLit struct {
	params []string
	body   *program
	pos    int
}

// funcLit reads a literal whose func keyword begins at pos; the
// caller has already seen the '(' that starts the parameter list. On
// success the parser's cursor moves past the closing brace.
func (p *Parser) funcLit(pos int) (arg, error) {
	end, err := funcLitEnd(p.src, pos)
	if err != nil {
		return arg{}, err
	}
	expr, err := goparser.ParseExpr(p.src[pos:end])
	if err != nil {
		return arg{}, funcLitErr(pos, err)
	}
	lit, ok := expr.(*ast.FuncLit)
	if !ok {
		return arg{}, fmt.Errorf("parse: expected a func literal at offset %d", pos)
	}
	lw := &lowering{base: pos}
	fl, err := lw.lowerLit(lit)
	if err != nil {
		return arg{}, err
	}
	p.pos = end
	p.nl = false
	return arg{kind: argFuncLit, fn: fl}, nil
}

// funcLitEnd scans from the func keyword to just past the matching
// closing brace. go/scanner does the token walk, so strings, comments
// and nested braces are already solved problems; the depth count only
// has to pair the brackets it reports.
func funcLitEnd(src string, start int) (int, error) {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src)-start)
	var s scanner.Scanner
	s.Init(file, []byte(src[start:]), nil, 0)
	depth, inBody := 0, false
	for {
		pos, tok, _ := s.Scan()
		if tok == token.EOF {
			return 0, fmt.Errorf("parse: unterminated func literal body at offset %d", start)
		}
		// Between the parameter list and the body the depth is zero;
		// the only token the literal continues with there is the brace.
		if !inBody && depth == 0 && tok != token.FUNC && tok != token.LPAREN {
			if tok != token.LBRACE {
				return 0, fmt.Errorf("parse: expected '{' to open the func literal body at offset %d", start+file.Offset(pos))
			}
			inBody = true
		}
		switch tok {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK:
			depth--
		case token.RBRACE:
			depth--
			if inBody && depth == 0 {
				return start + file.Offset(pos) + 1, nil
			}
		}
	}
}

// funcLitErr wraps go/parser's first error with the literal's offset
// in the program source; the line and column inside the message are
// go/parser's, relative to the func keyword.
func funcLitErr(pos int, err error) error {
	var el scanner.ErrorList
	if errors.As(err, &el) && len(el) > 0 {
		return fmt.Errorf("parse: func literal at offset %d: %s", pos, el[0])
	}
	return fmt.Errorf("parse: func literal at offset %d: %v", pos, err)
}

