package gozero

import (
	"fmt"
	"go/ast"
	"go/constant"
	goparser "go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"math"
	"strings"
)

// The expression right side, parsed by the standard library instead
// of a hand-written operator grammar. go/parser.ParseExpr reads the
// tree, so precedence, associativity, parentheses and every literal
// form arrive for free; go/types evaluates every constant subtree in
// the universe scope, so untyped-constant arithmetic, its faults
// (division by zero, mismatched types, shift rules) and its exactness
// (arbitrary-precision integers, rational floats rounded once) are
// the Go compiler's own, not a reimplementation. What survives to run
// time is unchanged from the one-operator grammar: a single +, == or
// != between a bound name and a value, compiled by vm_binop.go and
// stepjit_binop.go. Every operator go/parser knows is therefore
// recognized, and everything beyond the runtime trio must fold.

// goExprStmt finishes an assignment whose right side is a Go
// expression: "x := (2 + 3) * 4;" folds to a literal here, and
// "m := n + 2;" becomes the one runtime operator statement.
func (p *Parser) goExprStmt(lhs []string, define bool) (stmt, error) {
	text, err := p.captureExpr()
	if err != nil {
		return stmt{}, err
	}
	ex, err := goparser.ParseExpr(text)
	if err != nil {
		return stmt{}, fmt.Errorf("parse: %s: %s", text, goExprError(err))
	}
	st, err := convertGoExpr(text, ex)
	if err != nil {
		return stmt{}, err
	}
	st.lhs, st.define = lhs, define
	if !p.terminated() {
		return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
	}
	return st, nil
}

// captureExpr slices the raw right side out of the statement, up to
// the semicolon, line end or comment that closes it. Double-quoted
// strings hide their bytes from the scan; a single-quoted string is
// rejected by name, because go/parser would read it as a rune
// literal, and a raw string because the language has none.
func (p *Parser) captureExpr() (string, error) {
	p.skipSpace()
	start := p.pos
	i := p.pos
	inStr, esc := false, false
scan:
	for i < len(p.src) {
		c := p.src[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			i++
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '\'':
			return "", fmt.Errorf("parse: single-quoted strings do not take operators, use double quotes at offset %d", i)
		case '`':
			return "", fmt.Errorf("parse: raw strings are not in the grammar at offset %d", i)
		case ';', '\n':
			break scan
		case '/':
			if i+1 < len(p.src) && p.src[i+1] == '/' {
				break scan
			}
		}
		i++
	}
	text := strings.TrimSpace(p.src[start:i])
	if text == "" {
		return "", fmt.Errorf("parse: expected a value at offset %d", start)
	}
	p.pos = i
	return text, nil
}

// goExprError unwraps go/parser's error list to its first message,
// dropping the position prefix that points into a source file the
// program never had.
func goExprError(err error) string {
	if list, ok := err.(scanner.ErrorList); ok && len(list) > 0 {
		return list[0].Msg
	}
	return err.Error()
}

// convertGoExpr narrows the go/ast tree to what the language runs. A
// constant tree folds whole; otherwise the root is the one runtime
// operator and each operand is a bound name or a constant subtree,
// folded in place.
func convertGoExpr(text string, ex ast.Expr) (stmt, error) {
	root := ast.Unparen(ex)
	konst, err := constExpr(text, root)
	if err != nil {
		return stmt{}, err
	}
	if konst {
		a, err := foldConst(text, root)
		if err != nil {
			return stmt{}, err
		}
		return stmt{lit: &a}, nil
	}
	b, ok := root.(*ast.BinaryExpr)
	if !ok {
		if _, isName := root.(*ast.Ident); isName {
			return stmt{}, fmt.Errorf("parse: %s: cannot assign a name to a name", text)
		}
		return stmt{}, operandError(text, root)
	}
	var op string
	switch b.Op {
	case token.ADD:
		op = "+"
	case token.EQL:
		op = "=="
	case token.NEQ:
		op = "!="
	default:
		return stmt{}, fmt.Errorf("parse: %s: operator %s runs only in a constant expression, at runtime an assignment combines two values with +, == or !=", text, b.Op)
	}
	x, err := operandArg(text, b.X)
	if err != nil {
		return stmt{}, err
	}
	y, err := operandArg(text, b.Y)
	if err != nil {
		return stmt{}, err
	}
	return stmt{binOp: op, binX: &x, binY: &y}, nil
}

// operandArg resolves one side of the runtime operator: a constant
// subtree folds to a literal arg, a bare name stays a name, and a
// second runtime operator is rejected by rule, so no expression tree
// survives to the compiler.
func operandArg(text string, e ast.Expr) (arg, error) {
	e = ast.Unparen(e)
	konst, err := constExpr(text, e)
	if err != nil {
		return arg{}, err
	}
	if konst {
		return foldConst(text, e)
	}
	if id, ok := e.(*ast.Ident); ok {
		return arg{kind: argVar, str: id.Name}, nil
	}
	if _, nested := e.(*ast.BinaryExpr); nested {
		return arg{}, fmt.Errorf("parse: %s: one operator per assignment at runtime, bind %s to a name first", text, exprText(text, e))
	}
	return arg{}, operandError(text, e)
}

// constExpr reports whether a subtree is a constant expression:
// literals, true and false, and any operators over them. A bare name
// makes it non-constant; a node the language has no place for is an
// error naming the rule.
func constExpr(text string, e ast.Expr) (bool, error) {
	switch n := e.(type) {
	case *ast.BasicLit:
		switch n.Kind {
		case token.INT, token.FLOAT, token.STRING:
			return true, nil
		case token.IMAG:
			return false, fmt.Errorf("parse: %s: imaginary literals are not in the language", n.Value)
		}
		return false, fmt.Errorf("parse: %s: the literal form is not in the grammar", n.Value)
	case *ast.Ident:
		switch n.Name {
		case "true", "false":
			return true, nil
		case "nil":
			return false, fmt.Errorf("parse: %s: nil cannot be an operand", text)
		}
		return false, nil
	case *ast.ParenExpr:
		return constExpr(text, n.X)
	case *ast.UnaryExpr:
		switch n.Op {
		case token.ADD, token.SUB, token.NOT, token.XOR:
		case token.ARROW:
			return false, fmt.Errorf("parse: %s: a receive cannot be an operand, bind it with := first", text)
		case token.AND:
			return false, fmt.Errorf("parse: %s: & only prefixes a composite literal", text)
		default:
			return false, fmt.Errorf("parse: %s: unary %s is not in the grammar", text, n.Op)
		}
		konst, err := constExpr(text, n.X)
		if err != nil {
			return false, err
		}
		if !konst {
			return false, fmt.Errorf("parse: %s: unary %s applies to constants only, %s%s does not run", text, n.Op, n.Op, exprText(text, n.X))
		}
		return true, nil
	case *ast.BinaryExpr:
		xk, err := constExpr(text, n.X)
		if err != nil {
			return false, err
		}
		yk, err := constExpr(text, n.Y)
		if err != nil {
			return false, err
		}
		return xk && yk, nil
	}
	return false, operandError(text, e)
}

// operandError names the node kind that cannot stand in an
// expression, in the language's own terms.
func operandError(text string, e ast.Expr) error {
	spelled := exprText(text, e)
	switch e.(type) {
	case *ast.CallExpr:
		return fmt.Errorf("parse: %s: %s cannot be an operand, a call binds its result with := first", text, spelled)
	case *ast.SelectorExpr:
		return fmt.Errorf("parse: %s: %s cannot be an operand, bind the field to a name first", text, spelled)
	case *ast.CompositeLit:
		return fmt.Errorf("parse: %s: %s cannot be an operand, bind the composite to a name first", text, spelled)
	case *ast.IndexExpr, *ast.IndexListExpr, *ast.SliceExpr:
		return fmt.Errorf("parse: %s: indexing is not in the grammar", text)
	case *ast.StarExpr:
		return fmt.Errorf("parse: %s: a pointer does not dereference in an expression", text)
	}
	return fmt.Errorf("parse: %s: %s is not an operand, an operand is a bound name or a constant", text, spelled)
}

// foldConst evaluates a constant subtree with the type checker
// itself: types.Eval in the universe scope is go/constant arithmetic
// with Go's own rules and Go's own error messages. The fold lands at
// the language's literal widths, int64 and float64, with the same
// representability errors the Go compiler gives an assignment.
func foldConst(text string, e ast.Expr) (arg, error) {
	src := exprText(text, e)
	tv, err := types.Eval(token.NewFileSet(), nil, token.NoPos, src)
	if err != nil {
		return arg{}, fmt.Errorf("parse: constant %s: %s", src, typeErrMsg(err))
	}
	v := tv.Value
	if v == nil {
		return arg{}, fmt.Errorf("parse: %s does not fold to a constant", src)
	}
	switch v.Kind() {
	case constant.String:
		return arg{kind: argString, str: constant.StringVal(v)}, nil
	case constant.Bool:
		return arg{kind: argBool, b: constant.BoolVal(v)}, nil
	case constant.Int:
		i, exact := constant.Int64Val(v)
		if !exact {
			return arg{}, fmt.Errorf("parse: constant %s folds to %s, which overflows int64", src, v.ExactString())
		}
		return arg{kind: argInt, i: i}, nil
	case constant.Float:
		f, _ := constant.Float64Val(v)
		if math.IsInf(f, 0) {
			return arg{}, fmt.Errorf("parse: constant %s overflows float64", src)
		}
		return arg{kind: argFloat, f: f}, nil
	}
	return arg{}, fmt.Errorf("parse: constant %s has no type in the language", src)
}

// typeErrMsg strips the position prefix from a type-checker error:
// the message is Go's, the file it points into never existed.
func typeErrMsg(err error) string {
	if te, ok := err.(types.Error); ok {
		return te.Msg
	}
	return err.Error()
}

// exprText slices one node's source out of the expression text.
// Positions from a bare ParseExpr are 1-based byte offsets into it.
func exprText(text string, e ast.Expr) string {
	lo, hi := int(e.Pos())-1, int(e.End())-1
	if lo < 0 || hi > len(text) || lo >= hi {
		return text
	}
	return text[lo:hi]
}
