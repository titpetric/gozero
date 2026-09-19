package gozero

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/scanner"
	"go/token"
	"strconv"
)

// Conditions, the antithesis branch: the same if / else if / else
// surface as the L1 and L2 rungs, with the hand-rolled header parser
// replaced by Go's own. The bytes between "if" and the "{" that opens
// the block go to go/parser.ParseExpr, and the go/ast tree it returns
// is walked into the parser's argument forms. Statements and blocks
// stay line-oriented and hand-rolled; only the header expression is
// delegated.
//
//	ifstmt  := "if" header block [ "else" ( ifstmt | block ) ]
//	header  := go/parser.ParseExpr, restricted by the walk below to
//	           operand [ cmpop operand ]
//	block   := "{" { stmt } "}"
//
// The walk enforces the rung: one comparison or one bool operand per
// header. Everything else ParseExpr accepts is rejected by name:
// && and || and ! (composition), the arithmetic operators, and a
// comparison as an operand of another comparison.

// ifStmt is one if with its else chain. An else-if nests: els holds
// a single statement that is itself an if. Exactly one of cond and
// cmp is set.
type ifStmt struct {
	cond arg
	cmp  *cmpExpr
	then []stmt
	els  []stmt
}

// cmpExpr is a comparison in an if header: two operands and the
// operator between them. It exists only there; every other position
// rejects the operator by name (comparison placement).
type cmpExpr struct {
	op       string
	lhs, rhs arg
}

// parseIf reads an if statement after the keyword.
func (p *Parser) parseIf() (stmt, error) {
	is := &ifStmt{}
	hdr, off, err := p.ifHeader()
	if err != nil {
		return stmt{}, err
	}
	cond, cmp, err := headerCond(hdr, off)
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

// ifHeader captures the header source: everything from the keyword to
// the "{" that opens the block. The scan tracks parentheses, brackets
// and string literals, so a brace inside either does not end the
// header; a brace at depth zero does, which is Go's own composite
// literal restriction falling out of the capture. A newline at depth
// zero ends the line before the block opened and is an error; inside
// parentheses ParseExpr applies Go's own line rules.
func (p *Parser) ifHeader() (string, int, error) {
	p.skipSpace()
	start := p.pos
	depth := 0
	for i := p.pos; i < len(p.src); i++ {
		switch c := p.src[i]; c {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case '"', '\'':
			i++
			for i < len(p.src) && p.src[i] != c {
				if p.src[i] == '\\' {
					i++
				}
				i++
			}
			if i >= len(p.src) {
				return "", 0, fmt.Errorf("parse: unterminated string in an if header at offset %d", start)
			}
		case '`':
			i++
			for i < len(p.src) && p.src[i] != '`' {
				i++
			}
			if i >= len(p.src) {
				return "", 0, fmt.Errorf("parse: unterminated string in an if header at offset %d", start)
			}
		case '\n':
			if depth == 0 {
				return "", 0, fmt.Errorf("parse: an if header and its '{' share a line at offset %d", i)
			}
		case '{':
			if depth > 0 {
				depth++
				continue
			}
			if i == start {
				return "", 0, fmt.Errorf("parse: expected an if condition at offset %d", start)
			}
			p.pos = i
			p.nl = false
			return p.src[start:i], start, nil
		case '}':
			depth--
		}
	}
	return "", 0, fmt.Errorf("parse: unterminated if header at offset %d", start)
}

// headerCond parses the captured header with go/parser and walks the
// expression into the rung's condition: one operand standing as a
// bool, or one comparison between two. A bare literal is caught here:
// it can only be a comparison's side, never the whole condition.
func headerCond(hdr string, off int) (arg, *cmpExpr, error) {
	e, err := goparser.ParseExpr(hdr)
	if err != nil {
		return arg{}, nil, headerErr(err, off)
	}
	w := headerWalker{off: off}
	e = ast.Unparen(e)
	if be, ok := e.(*ast.BinaryExpr); ok {
		op, ok := cmpTok(be.Op)
		if !ok {
			return arg{}, nil, w.rejectOp(be.Op, be)
		}
		lhs, err := w.operand(be.X)
		if err != nil {
			return arg{}, nil, err
		}
		rhs, err := w.operand(be.Y)
		if err != nil {
			return arg{}, nil, err
		}
		return arg{}, &cmpExpr{op: op, lhs: lhs, rhs: rhs}, nil
	}
	a, err := w.operand(e)
	if err != nil {
		return arg{}, nil, err
	}
	switch a.kind {
	case argString, argInt, argFloat:
		return arg{}, nil, fmt.Errorf("parse: a literal is not an if condition at offset %d", w.pos(e))
	case argStruct, argRecv:
		return arg{}, nil, fmt.Errorf("parse: an if condition is a bool name, a bool field, or a call returning bool (condition form) at offset %d", w.pos(e))
	}
	return a, nil, nil
}

// headerErr rewrites a go/parser error into the parser's own shape,
// with the offset moved from the header slice to the program source.
// ParseExpr documents its error type as a scanner.ErrorList.
func headerErr(err error, off int) error {
	if list, ok := err.(scanner.ErrorList); ok && len(list) > 0 {
		e := list[0]
		return fmt.Errorf("parse: if header: %s at offset %d", e.Msg, off+e.Pos.Offset)
	}
	return fmt.Errorf("parse: if header: %v", err)
}

// headerWalker carries the header's offset in the program source, so
// a rejection points at the program rather than the captured slice.
type headerWalker struct {
	off int
}

// pos converts an ast position, 1-based in the header slice, to a
// program offset.
func (w headerWalker) pos(n ast.Node) int {
	return w.off + int(n.Pos()) - 1
}

// cmpTok admits the six comparison tokens.
func cmpTok(t token.Token) (string, bool) {
	switch t {
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
		return t.String(), true
	}
	return "", false
}

// rejectOp names the rule an out-of-rung operator breaks: composition
// for the boolean connectives, arithmetic for the rest. ParseExpr has
// already accepted the whole of Go's expression grammar; this walk is
// where the rung narrows it.
func (w headerWalker) rejectOp(op token.Token, n ast.Node) error {
	switch op {
	case token.LAND, token.LOR, token.NOT:
		return fmt.Errorf("parse: %s is not in this rung (composition), an if header takes one comparison or one bool operand at offset %d", op, w.pos(n))
	}
	return fmt.Errorf("parse: %s is not in this rung (arithmetic), an if header takes one comparison or one bool operand at offset %d", op, w.pos(n))
}

// operand walks one condition or comparison operand: a literal, a
// name, a dotted path, a call, a composite literal, or a receive.
// The set mirrors what the hand-rolled argument reader accepts in the
// same position.
func (w headerWalker) operand(e ast.Expr) (arg, error) {
	e = ast.Unparen(e)
	switch v := e.(type) {
	case *ast.BasicLit:
		return w.lit(v, false)
	case *ast.Ident:
		return arg{kind: argVar, str: v.Name}, nil
	case *ast.SelectorExpr:
		if path, ok := selectorPath(v); ok {
			return arg{kind: argPath, path: path}, nil
		}
		return arg{}, fmt.Errorf("parse: a dotted name roots in a name at offset %d", w.pos(v))
	case *ast.CallExpr:
		call, err := w.call(v)
		if err != nil {
			return arg{}, err
		}
		return arg{kind: argCall, sub: call}, nil
	case *ast.CompositeLit:
		return w.composite(v, false)
	case *ast.UnaryExpr:
		return w.unary(v)
	case *ast.BinaryExpr:
		if _, ok := cmpTok(v.Op); ok {
			return arg{}, fmt.Errorf("parse: a comparison does not compare (comparison operand) at offset %d", w.pos(v))
		}
		return arg{}, w.rejectOp(v.Op, v)
	}
	return arg{}, fmt.Errorf("parse: this form is not an if header operand at offset %d", w.pos(e))
}

// unary walks the unary forms the grammar owns elsewhere: a negative
// number, &T{}, and a receive. ! is the composition rejection.
func (w headerWalker) unary(v *ast.UnaryExpr) (arg, error) {
	switch v.Op {
	case token.SUB:
		if bl, ok := ast.Unparen(v.X).(*ast.BasicLit); ok && (bl.Kind == token.INT || bl.Kind == token.FLOAT) {
			return w.lit(bl, true)
		}
		return arg{}, fmt.Errorf("parse: unary minus negates a number literal only at offset %d", w.pos(v))
	case token.AND:
		if cl, ok := ast.Unparen(v.X).(*ast.CompositeLit); ok {
			return w.composite(cl, true)
		}
		return arg{}, fmt.Errorf("parse: & takes a composite literal only at offset %d", w.pos(v))
	case token.ARROW:
		sub, err := w.operand(v.X)
		if err != nil {
			return arg{}, err
		}
		return arg{kind: argRecv, recv: &sub}, nil
	}
	return arg{}, w.rejectOp(v.Op, v)
}

// lit converts a Go literal token to the parser's literal forms. The
// numeric spellings are Go's own: hex, binary, octal and underscores
// come with strconv's base-0 parse, none of which the line grammar's
// lexer reads. A rune literal has no place in a language whose only
// character data is a string.
func (w headerWalker) lit(v *ast.BasicLit, neg bool) (arg, error) {
	text := v.Value
	switch v.Kind {
	case token.INT:
		if neg {
			text = "-" + text
		}
		i, err := strconv.ParseInt(text, 0, 64)
		if err != nil {
			return arg{}, fmt.Errorf("parse: bad int %q at offset %d", text, w.pos(v))
		}
		return arg{kind: argInt, i: i}, nil
	case token.FLOAT:
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return arg{}, fmt.Errorf("parse: bad float %q at offset %d", text, w.pos(v))
		}
		if neg {
			f = -f
		}
		return arg{kind: argFloat, f: f}, nil
	case token.STRING:
		s, err := strconv.Unquote(text)
		if err != nil {
			return arg{}, fmt.Errorf("parse: bad string %s at offset %d", text, w.pos(v))
		}
		return arg{kind: argString, str: s}, nil
	case token.CHAR:
		return arg{}, fmt.Errorf("parse: a rune literal is not in the language, quote a string at offset %d", w.pos(v))
	}
	return arg{}, fmt.Errorf("parse: a %s literal is not in the language at offset %d", v.Kind, w.pos(v))
}

// call walks a call and the method chain on its result. The function
// position is a name, a dotted path, or another call a selector
// chains on, the same three forms the statement grammar's expr reads.
func (w headerWalker) call(c *ast.CallExpr) (*callExpr, error) {
	args, err := w.args(c)
	if err != nil {
		return nil, err
	}
	switch fun := ast.Unparen(c.Fun).(type) {
	case *ast.Ident:
		return &callExpr{path: []string{fun.Name}, args: args}, nil
	case *ast.SelectorExpr:
		if path, ok := selectorPath(fun); ok {
			return &callExpr{path: path, args: args}, nil
		}
		if inner, ok := ast.Unparen(fun.X).(*ast.CallExpr); ok {
			root, err := w.call(inner)
			if err != nil {
				return nil, err
			}
			root.chain = append(root.chain, link{name: fun.Sel.Name, args: args})
			return root, nil
		}
	}
	return nil, fmt.Errorf("parse: a call names a binding or chains on a call at offset %d", w.pos(c))
}

// args walks a call's arguments; a trailing ellipsis marks the last
// one spread.
func (w headerWalker) args(c *ast.CallExpr) ([]arg, error) {
	if len(c.Args) == 0 {
		return nil, nil
	}
	out := make([]arg, 0, len(c.Args))
	for _, e := range c.Args {
		a, err := w.operand(e)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if c.Ellipsis.IsValid() {
		out[len(out)-1].spread = true
	}
	return out, nil
}

// composite walks a composite literal: the type path, then keyed or
// positional elements.
func (w headerWalker) composite(cl *ast.CompositeLit, addr bool) (arg, error) {
	var path []string
	switch t := cl.Type.(type) {
	case *ast.Ident:
		path = []string{t.Name}
	case *ast.SelectorExpr:
		p, ok := selectorPath(t)
		if !ok {
			return arg{}, fmt.Errorf("parse: a composite literal names its type at offset %d", w.pos(cl))
		}
		path = p
	default:
		return arg{}, fmt.Errorf("parse: a composite literal names its type at offset %d", w.pos(cl))
	}
	out := arg{kind: argStruct, path: path, addr: addr}
	for _, el := range cl.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				return arg{}, fmt.Errorf("parse: a composite literal keys on a field name at offset %d", w.pos(kv))
			}
			val, err := w.operand(kv.Value)
			if err != nil {
				return arg{}, err
			}
			out.elems = append(out.elems, structElem{name: key.Name, val: val})
			continue
		}
		val, err := w.operand(el)
		if err != nil {
			return arg{}, err
		}
		out.elems = append(out.elems, structElem{val: val})
	}
	return out, nil
}

// selectorPath flattens a selector chain of plain names, req.URL.Path,
// into its segments. A chain rooted anywhere else reports false and
// the caller decides: a call chains, everything else rejects.
func selectorPath(s *ast.SelectorExpr) ([]string, bool) {
	var rev []string
	for {
		rev = append(rev, s.Sel.Name)
		switch x := ast.Unparen(s.X).(type) {
		case *ast.Ident:
			rev = append(rev, x.Name)
			for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
				rev[i], rev[j] = rev[j], rev[i]
			}
			return rev, true
		case *ast.SelectorExpr:
			s = x
		default:
			return nil, false
		}
	}
}

// rejectCmp fails with the placement rule when a comparison operator
// follows, which is how "y := x == 5" names its error instead of
// surfacing as a strange assignment. '<' immediately followed by '-'
// is the channel arrow, never a comparison, matching Go's tokenizer.
func (p *Parser) rejectCmp() error {
	save, saveNL := p.pos, p.nl
	p.skipSpace()
	at, cmp := p.pos, false
	if p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '=', '!':
			cmp = p.pos+1 < len(p.src) && p.src[p.pos+1] == '='
		case '<':
			cmp = p.pos+1 >= len(p.src) || p.src[p.pos+1] != '-'
		case '>':
			cmp = true
		}
	}
	p.pos, p.nl = save, saveNL
	if cmp {
		return fmt.Errorf("parse: a comparison is only legal in an if header (comparison placement) at offset %d", at)
	}
	return nil
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
