package gozero

// The for statement is the one place the front end leans on the
// standard library: go/scanner finds the loop's extent, go/parser
// parses that extent as Go inside a synthetic function body, and the
// ast lowers into the rangeStmt the compiler consumes. The statement
// grammar stays hand-rolled: a body statement that is not a loop, a
// branch, a return or a declaration is re-parsed by Parser.stmt from
// its source offset, so its shape and its error messages are the same
// inside a body and outside one.

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"strconv"
)

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

// loopWrap wraps a loop so parser.ParseFile accepts it: Go has no
// statement-level entry point, so the loop parses as the body of a
// synthetic function. Offsets into the wrapped file map back to the
// program by subtracting the prefix length.
const loopWrap = "package p\nfunc _() {\n"

// forGo parses one for statement whose "for" keyword starts at
// src[start]. The extent scan guarantees the wrapped source holds
// exactly the loop, so the synthetic body has exactly one statement.
func (p *Parser) forGo(start int) (stmt, error) {
	end, err := p.loopExtent(start)
	if err != nil {
		return stmt{}, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "loop.go", loopWrap+p.src[start:end]+"\n}\n", parser.SkipObjectResolution)
	if err != nil {
		return stmt{}, goParseError(err, start)
	}
	list := f.Decls[0].(*ast.FuncDecl).Body.List
	if len(list) != 1 {
		return stmt{}, fmt.Errorf("parse: expected one for statement at offset %d", start)
	}
	l := &lowerer{src: p.src, fset: fset, start: start}
	s, err := l.loop(list[0])
	if err != nil {
		return stmt{}, err
	}
	p.pos = end
	p.nl = false
	if !p.terminated() {
		return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
	}
	return s, nil
}

// loopExtent scans tokens from the for keyword and returns the offset
// just past the brace that closes the loop body. The body's opening
// brace is the first one outside parentheses and brackets, which is
// Go's own rule: a composite literal at the top level of a for header
// must be parenthesized.
func (p *Parser) loopExtent(start int) (int, error) {
	fset := token.NewFileSet()
	file := fset.AddFile("loop.go", -1, len(p.src)-start)
	var s scanner.Scanner
	s.Init(file, []byte(p.src[start:]), nil, 0)
	nest, braces, inBody := 0, 0, false
	for {
		pos, tok, _ := s.Scan()
		switch tok {
		case token.EOF:
			return 0, fmt.Errorf("parse: unterminated range body at offset %d", len(p.src))
		case token.LPAREN, token.LBRACK:
			nest++
		case token.RPAREN, token.RBRACK:
			nest--
		case token.LBRACE:
			braces++
			if !inBody && nest == 0 {
				inBody = true
			}
		case token.RBRACE:
			braces--
			if inBody && braces == 0 {
				return start + file.Offset(pos) + 1, nil
			}
		}
	}
}

// goParseError rewrites go/parser's first error to carry a program
// offset. The message stays go/parser's own: what the standard
// grammar rejects, it also gets to name.
func goParseError(err error, start int) error {
	var list scanner.ErrorList
	if errors.As(err, &list) && len(list) > 0 {
		e := list[0]
		off := e.Pos.Offset - len(loopWrap) + start
		if off < start {
			off = start
		}
		return fmt.Errorf("parse: %s (offset %d)", e.Msg, off)
	}
	return fmt.Errorf("parse: %v", err)
}

// lowerer turns the parsed ast back into the parser's own statement
// and argument forms. start maps wrapped-file offsets to program
// offsets, and src lets a body statement re-enter Parser.stmt at its
// own position, so offsets in its errors stay program offsets.
type lowerer struct {
	src   string
	fset  *token.FileSet
	start int
}

// off maps an ast position back to an offset in the program source.
func (l *lowerer) off(pos token.Pos) int {
	return l.fset.Position(pos).Offset - len(loopWrap) + l.start
}

// loop lowers a for statement. Only the range form is in the
// language: the three-clause and condition forms parse as Go but need
// operator expressions the grammar does not have.
func (l *lowerer) loop(s ast.Stmt) (stmt, error) {
	switch s := s.(type) {
	case *ast.RangeStmt:
		return l.rangeLoop(s)
	case *ast.ForStmt:
		return stmt{}, fmt.Errorf("parse: for supports only the range form at offset %d", l.off(s.For))
	}
	return stmt{}, fmt.Errorf("parse: expected a for statement at offset %d", l.off(s.Pos()))
}

// rangeLoop lowers a range statement: the iteration names, the ranged
// expression, and the body statement by statement.
func (l *lowerer) rangeLoop(rs *ast.RangeStmt) (stmt, error) {
	r := &rangeStmt{}
	if rs.Tok == token.ASSIGN {
		return stmt{}, fmt.Errorf("parse: a range loop declares its names with := at offset %d", l.off(rs.TokPos))
	}
	var err error
	if r.key, err = l.loopName(rs.Key); err != nil {
		return stmt{}, err
	}
	if r.val, err = l.loopName(rs.Value); err != nil {
		return stmt{}, err
	}
	over, err := l.expr(rs.X)
	if err != nil {
		return stmt{}, err
	}
	switch over.kind {
	case argVar, argPath, argCall, argInt, argString:
	default:
		return stmt{}, fmt.Errorf("parse: cannot range over this expression at offset %d", l.off(rs.X.Pos()))
	}
	r.over = over
	for _, bs := range rs.Body.List {
		st, err := l.bodyStmt(bs)
		if err != nil {
			return stmt{}, err
		}
		if st != nil {
			r.body = append(r.body, *st)
		}
	}
	return stmt{rng: r}, nil
}

// loopName reads an iteration variable, which is a plain name or the
// blank identifier; nil is the absent name of a shorter header.
func (l *lowerer) loopName(e ast.Expr) (string, error) {
	if e == nil {
		return "", nil
	}
	id, ok := e.(*ast.Ident)
	if !ok {
		return "", fmt.Errorf("parse: a range loop binds plain names at offset %d", l.off(e.Pos()))
	}
	return id.Name, nil
}

// bodyStmt lowers one statement of a loop body. The statement kinds a
// body cannot hold are rejected here, on the ast's say-so; a nested
// loop lowers recursively; everything else re-parses from its source
// offset through the hand-rolled statement grammar.
func (l *lowerer) bodyStmt(s ast.Stmt) (*stmt, error) {
	switch s := s.(type) {
	case *ast.RangeStmt, *ast.ForStmt:
		st, err := l.loop(s)
		if err != nil {
			return nil, err
		}
		return &st, nil
	case *ast.BranchStmt:
		return l.branch(s)
	case *ast.ReturnStmt:
		return nil, fmt.Errorf("parse: return cannot stand inside a range body, a body runs every statement of every iteration (offset %d)", l.off(s.Pos()))
	case *ast.DeclStmt:
		return nil, fmt.Errorf("parse: a var declaration cannot stand inside a range body, declare the name before the loop (offset %d)", l.off(s.Pos()))
	case *ast.EmptyStmt:
		return nil, nil
	}
	sub := &Parser{src: l.src, pos: l.off(s.Pos())}
	st, err := sub.stmt()
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// branch lowers break and continue. Both exit the innermost loop
// only, so a label is rejected by name; the other branch keywords are
// not in the language.
func (l *lowerer) branch(s *ast.BranchStmt) (*stmt, error) {
	switch s.Tok {
	case token.BREAK, token.CONTINUE:
	default:
		return nil, fmt.Errorf("parse: %s is not in the language (offset %d)", s.Tok, l.off(s.Pos()))
	}
	if s.Label != nil {
		kw := s.Tok.String()
		return nil, fmt.Errorf("parse: a label after %s is not in the language, %s exits the innermost loop (offset %d)", kw, kw, l.off(s.Label.Pos()))
	}
	if s.Tok == token.BREAK {
		return &stmt{brk: true}, nil
	}
	return &stmt{cont: true}, nil
}

// expr lowers a range header expression into the parser's arg form.
// The set of forms is the argument grammar's: names, dotted paths,
// literals, calls and their chains, composites, receives.
func (l *lowerer) expr(e ast.Expr) (arg, error) {
	switch e := e.(type) {
	case *ast.Ident:
		switch e.Name {
		case "true":
			return arg{kind: argBool, b: true}, nil
		case "false":
			return arg{kind: argBool}, nil
		case "nil":
			return arg{kind: argNil}, nil
		}
		return arg{kind: argVar, str: e.Name}, nil
	case *ast.SelectorExpr:
		path, err := l.path(e)
		if err != nil {
			return arg{}, err
		}
		return arg{kind: argPath, path: path}, nil
	case *ast.BasicLit:
		return l.lit(e)
	case *ast.CallExpr:
		sub, err := l.call(e)
		if err != nil {
			return arg{}, err
		}
		return arg{kind: argCall, sub: sub}, nil
	case *ast.CompositeLit:
		return l.composite(e, false)
	case *ast.ParenExpr:
		return l.expr(e.X)
	case *ast.UnaryExpr:
		return l.unary(e)
	}
	return arg{}, fmt.Errorf("parse: this expression is not in the language (offset %d)", l.off(e.Pos()))
}

// unary lowers the three prefixes the argument grammar has: a
// negative number, a channel receive, and the address of a composite
// literal.
func (l *lowerer) unary(e *ast.UnaryExpr) (arg, error) {
	switch e.Op {
	case token.SUB:
		a, err := l.expr(e.X)
		if err != nil {
			return arg{}, err
		}
		switch a.kind {
		case argInt:
			a.i = -a.i
			return a, nil
		case argFloat:
			a.f = -a.f
			return a, nil
		}
	case token.ARROW:
		src, err := l.expr(e.X)
		if err != nil {
			return arg{}, err
		}
		switch src.kind {
		case argVar, argPath, argCall:
			return arg{kind: argRecv, recv: &src}, nil
		}
		return arg{}, fmt.Errorf("parse: expected a channel after <- at offset %d", l.off(e.X.Pos()))
	case token.AND:
		if cl, ok := e.X.(*ast.CompositeLit); ok {
			return l.composite(cl, true)
		}
		return arg{}, fmt.Errorf("parse: expected a composite literal after '&' at offset %d", l.off(e.X.Pos()))
	}
	return arg{}, fmt.Errorf("parse: this expression is not in the language (offset %d)", l.off(e.Pos()))
}

// lit lowers a literal. A char literal lowers to a string: the
// hand-rolled grammar reads single quotes as a string form.
func (l *lowerer) lit(e *ast.BasicLit) (arg, error) {
	switch e.Kind {
	case token.INT:
		i, err := strconv.ParseInt(e.Value, 0, 64)
		if err != nil {
			return arg{}, fmt.Errorf("parse: bad int %q: %w", e.Value, err)
		}
		return arg{kind: argInt, i: i}, nil
	case token.FLOAT:
		f, err := strconv.ParseFloat(e.Value, 64)
		if err != nil {
			return arg{}, fmt.Errorf("parse: bad float %q: %w", e.Value, err)
		}
		return arg{kind: argFloat, f: f}, nil
	case token.STRING:
		s, err := strconv.Unquote(e.Value)
		if err != nil {
			return arg{}, fmt.Errorf("parse: bad string at offset %d: %w", l.off(e.Pos()), err)
		}
		return arg{kind: argString, str: s}, nil
	case token.CHAR:
		s, err := strconv.Unquote(e.Value)
		if err != nil {
			return arg{}, fmt.Errorf("parse: bad string at offset %d: %w", l.off(e.Pos()), err)
		}
		return arg{kind: argString, str: s}, nil
	}
	return arg{}, fmt.Errorf("parse: this literal is not in the language (offset %d)", l.off(e.Pos()))
}

// path unwinds a selector chain into the dotted-name form, "a.b.c".
func (l *lowerer) path(e ast.Expr) ([]string, error) {
	switch e := e.(type) {
	case *ast.Ident:
		return []string{e.Name}, nil
	case *ast.SelectorExpr:
		base, err := l.path(e.X)
		if err != nil {
			return nil, err
		}
		return append(base, e.Sel.Name), nil
	}
	return nil, fmt.Errorf("parse: expected a dotted name at offset %d", l.off(e.Pos()))
}

// call lowers a call and the chain of method calls on its result:
// f(x).M(y) is the call of f with a link for M, the shape the
// compiler resolves.
func (l *lowerer) call(e *ast.CallExpr) (*callExpr, error) {
	args, err := l.args(e)
	if err != nil {
		return nil, err
	}
	if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
		if inner, ok := sel.X.(*ast.CallExpr); ok {
			ce, err := l.call(inner)
			if err != nil {
				return nil, err
			}
			ce.chain = append(ce.chain, link{name: sel.Sel.Name, args: args})
			return ce, nil
		}
	}
	path, err := l.path(e.Fun)
	if err != nil {
		return nil, fmt.Errorf("parse: expected a call at offset %d", l.off(e.Fun.Pos()))
	}
	return &callExpr{path: path, args: args}, nil
}

// args lowers a call's arguments. Go marks the spread on the call;
// the arg form carries it on the last argument.
func (l *lowerer) args(e *ast.CallExpr) ([]arg, error) {
	var out []arg
	for _, x := range e.Args {
		a, err := l.expr(x)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if e.Ellipsis.IsValid() && len(out) > 0 {
		out[len(out)-1].spread = true
	}
	return out, nil
}

// composite lowers a composite literal. The compiler checks that
// keyed and positional elements are not mixed, because only it can
// name the fields.
func (l *lowerer) composite(e *ast.CompositeLit, addr bool) (arg, error) {
	if e.Type == nil {
		return arg{}, fmt.Errorf("parse: a composite literal names its type at offset %d", l.off(e.Pos()))
	}
	path, err := l.path(e.Type)
	if err != nil {
		return arg{}, err
	}
	a := arg{kind: argStruct, path: path, addr: addr}
	for _, el := range e.Elts {
		var se structElem
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				return arg{}, fmt.Errorf("parse: expected a field name at offset %d", l.off(kv.Key.Pos()))
			}
			se.name = key.Name
			el = kv.Value
		}
		v, err := l.expr(el)
		if err != nil {
			return arg{}, err
		}
		se.val = v
		a.elems = append(a.elems, se)
	}
	return a, nil
}
