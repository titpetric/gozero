package gozero

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
)

// lowering walks a parsed literal's ast into the structures the
// compiler reads. base is the offset of the outer literal's func
// keyword in the program source, which is what turns go/parser's
// positions back into the byte offsets every other parse error uses.
type lowering struct {
	base int
}

func (lw *lowering) off(n ast.Node) int {
	return lw.base + int(n.Pos()) - 1
}

func (lw *lowering) errf(n ast.Node, format string, args ...any) error {
	return fmt.Errorf("parse: %s at offset %d", fmt.Sprintf(format, args...), lw.off(n))
}

// lowerLit turns one ast.FuncLit into a funcLit. A nested literal in
// the body comes back through here with the same base: its positions
// live in the same parse.
func (lw *lowering) lowerLit(lit *ast.FuncLit) (*funcLit, error) {
	fl := &funcLit{pos: lw.off(lit)}
	ft := lit.Type
	if ft.Results != nil {
		return nil, lw.errf(ft.Results, "result types come from the target signature and are not written")
	}
	for _, f := range ft.Params.List {
		// func(w, r) parses as unnamed parameters typed w and r; those
		// idents are the names. A field with names carries written
		// types, func(w http.ResponseWriter), which the grammar leaves
		// to the target signature.
		id, ok := f.Type.(*ast.Ident)
		if len(f.Names) > 0 || !ok {
			return nil, lw.errf(f, "parameter types come from the target signature and are not written")
		}
		fl.params = append(fl.params, id.Name)
	}
	body := &program{}
	for _, st := range lit.Body.List {
		if _, ok := st.(*ast.EmptyStmt); ok {
			continue
		}
		s, err := lw.stmt(st)
		if err != nil {
			return nil, err
		}
		body.stmts = append(body.stmts, s)
	}
	fl.body = body
	return fl, nil
}

// stmt lowers one body statement. The body is the same straight-line
// statement list a program is; go/parser accepts every Go statement,
// so each form outside the grammar is rejected here by name.
func (lw *lowering) stmt(st ast.Stmt) (stmt, error) {
	switch s := st.(type) {
	case *ast.ExprStmt:
		a, err := lw.arg(s.X)
		if err != nil {
			return stmt{}, err
		}
		switch a.kind {
		case argCall:
			return stmt{call: a.sub}, nil
		case argRecv:
			// A bare receive runs for its blocking effect.
			return stmt{lit: &a}, nil
		}
		return stmt{}, lw.errf(s, "a statement is a call, an assignment, a var declaration, a send, a receive, or a return")

	case *ast.AssignStmt:
		return lw.assign(s)

	case *ast.DeclStmt:
		return lw.decl(s)

	case *ast.ReturnStmt:
		if len(s.Results) == 0 {
			return stmt{ret: true}, nil
		}
		if len(s.Results) > 1 {
			return stmt{}, lw.errf(s, "a body returns one value")
		}
		a, err := lw.arg(s.Results[0])
		if err != nil {
			return stmt{}, err
		}
		if a.kind == argCall {
			return stmt{ret: true, call: a.sub}, nil
		}
		return stmt{ret: true, retVal: &a}, nil

	case *ast.SendStmt:
		path, ok := exprPath(s.Chan)
		if !ok {
			return stmt{}, lw.errf(s.Chan, "a send names its channel")
		}
		v, err := lw.arg(s.Value)
		if err != nil {
			return stmt{}, err
		}
		return stmt{sendCh: path, sendVal: &v}, nil
	}
	return stmt{}, lw.errf(st, "%s is not in the statement grammar; the body is the straight-line list a program is", stmtName(st))
}

// assign lowers x, y := f(), req.Method = v and every literal
// assignment. The rules are the byte-level grammar's own, applied to
// the ast instead of the source.
func (lw *lowering) assign(s *ast.AssignStmt) (stmt, error) {
	if s.Tok != token.DEFINE && s.Tok != token.ASSIGN {
		return stmt{}, lw.errf(s, "the grammar has no operators")
	}
	if len(s.Rhs) != 1 {
		return stmt{}, lw.errf(s, "one value stands on the right of an assignment")
	}

	// A dotted path on the left is a field assignment.
	if len(s.Lhs) == 1 {
		if sel, ok := s.Lhs[0].(*ast.SelectorExpr); ok {
			path, ok := exprPath(sel)
			if !ok {
				return stmt{}, lw.errf(sel, "a field assignment names its target")
			}
			if s.Tok == token.DEFINE {
				return stmt{}, lw.errf(s, "a field cannot be declared with :=")
			}
			a, err := lw.arg(s.Rhs[0])
			if err != nil {
				return stmt{}, err
			}
			if a.kind == argVar || a.kind == argPath {
				return stmt{}, lw.errf(s, "cannot assign a name to a field")
			}
			if a.kind == argCall {
				return stmt{fieldLhs: path, call: a.sub}, nil
			}
			return stmt{fieldLhs: path, lit: &a}, nil
		}
	}

	lhs := make([]string, len(s.Lhs))
	for i, l := range s.Lhs {
		id, ok := l.(*ast.Ident)
		if !ok {
			return stmt{}, lw.errf(l, "only names stand on the left of an assignment")
		}
		lhs[i] = id.Name
	}
	a, err := lw.arg(s.Rhs[0])
	if err != nil {
		return stmt{}, err
	}
	switch a.kind {
	case argCall:
		return stmt{lhs: lhs, define: s.Tok == token.DEFINE, call: a.sub}, nil
	case argVar, argPath:
		return stmt{}, lw.errf(s, "cannot assign a name to a name")
	}
	return stmt{lhs: lhs, define: s.Tok == token.DEFINE, lit: &a}, nil
}

// decl lowers "var name Type". types.ExprString renders the ast type
// back to the spelling reflect.Type.String produces, which is what
// the registry is keyed by: *url.URL, []string, chan<- string.
func (lw *lowering) decl(s *ast.DeclStmt) (stmt, error) {
	gd, ok := s.Decl.(*ast.GenDecl)
	if !ok || gd.Tok != token.VAR {
		return stmt{}, lw.errf(s, "only var declares a name in a body")
	}
	if len(gd.Specs) != 1 {
		return stmt{}, lw.errf(s, "var declares one name")
	}
	vs, ok := gd.Specs[0].(*ast.ValueSpec)
	if !ok || len(vs.Names) != 1 || vs.Type == nil || len(vs.Values) != 0 {
		return stmt{}, lw.errf(s, "var takes one name and a type; an initial value is an assignment")
	}
	return stmt{varName: vs.Names[0].Name, varType: types.ExprString(vs.Type)}, nil
}

// arg lowers one expression into the argument grammar.
func (lw *lowering) arg(e ast.Expr) (arg, error) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		// Parentheses group a single value; there are no operators to
		// give them precedence over.
		return lw.arg(x.X)

	case *ast.BasicLit:
		return lw.basicLit(x)

	case *ast.Ident:
		switch x.Name {
		case "true":
			return arg{kind: argBool, b: true}, nil
		case "false":
			return arg{kind: argBool}, nil
		case "nil":
			return arg{kind: argNil}, nil
		}
		return arg{kind: argVar, str: x.Name}, nil

	case *ast.SelectorExpr:
		path, ok := exprPath(x)
		if !ok {
			return arg{}, lw.errf(x, "only a dotted name selects a field")
		}
		return arg{kind: argPath, path: path}, nil

	case *ast.CallExpr:
		sub, err := lw.call(x)
		if err != nil {
			return arg{}, err
		}
		return arg{kind: argCall, sub: sub}, nil

	case *ast.UnaryExpr:
		return lw.unary(x)

	case *ast.CompositeLit:
		return lw.composite(x, false)

	case *ast.FuncLit:
		fl, err := lw.lowerLit(x)
		if err != nil {
			return arg{}, err
		}
		return arg{kind: argFuncLit, fn: fl}, nil

	case *ast.BinaryExpr:
		return arg{}, lw.errf(x, "the grammar has no operators")
	}
	return arg{}, lw.errf(e, "%s is not in the argument grammar", types.ExprString(e))
}

// unary lowers the three prefixes the grammar knows: a receive, an
// addressed composite literal, and a negative number.
func (lw *lowering) unary(x *ast.UnaryExpr) (arg, error) {
	switch x.Op {
	case token.ARROW:
		src, err := lw.arg(x.X)
		if err != nil {
			return arg{}, err
		}
		switch src.kind {
		case argVar, argPath, argCall:
		default:
			return arg{}, lw.errf(x, "expected a channel after <-")
		}
		return arg{kind: argRecv, recv: &src}, nil
	case token.AND:
		cl, ok := x.X.(*ast.CompositeLit)
		if !ok {
			return arg{}, lw.errf(x, "expected a composite literal after '&'")
		}
		return lw.composite(cl, true)
	case token.SUB:
		bl, ok := x.X.(*ast.BasicLit)
		if !ok {
			return arg{}, lw.errf(x, "the grammar has no operators")
		}
		a, err := lw.basicLit(bl)
		if err != nil {
			return arg{}, err
		}
		switch a.kind {
		case argInt:
			a.i = -a.i
		case argFloat:
			a.f = -a.f
		default:
			return arg{}, lw.errf(x, "only a number negates")
		}
		return a, nil
	}
	return arg{}, lw.errf(x, "the grammar has no operators")
}

// call flattens a call and the method chain on its result into the
// grammar's callExpr: path "(" args ")" { "." ident "(" args ")" }.
func (lw *lowering) call(x *ast.CallExpr) (*callExpr, error) {
	args, err := lw.args(x)
	if err != nil {
		return nil, err
	}
	switch fn := x.Fun.(type) {
	case *ast.Ident:
		return &callExpr{path: []string{fn.Name}, args: args}, nil
	case *ast.SelectorExpr:
		if path, ok := exprPath(fn); ok {
			return &callExpr{path: path, args: args}, nil
		}
		// A method called on a call's result chains onto it.
		if inner, ok := fn.X.(*ast.CallExpr); ok {
			base, err := lw.call(inner)
			if err != nil {
				return nil, err
			}
			base.chain = append(base.chain, link{name: fn.Sel.Name, args: args})
			return base, nil
		}
	}
	return nil, lw.errf(x, "a call names a binding, a method on a name, or a method on a call's result")
}

func (lw *lowering) args(x *ast.CallExpr) ([]arg, error) {
	var out []arg
	for _, e := range x.Args {
		a, err := lw.arg(e)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if x.Ellipsis.IsValid() && len(out) > 0 {
		out[len(out)-1].spread = true
	}
	return out, nil
}

// composite lowers T{...} and &T{...}. The path names the type; the
// compiler resolves it, because only it holds the registry.
func (lw *lowering) composite(x *ast.CompositeLit, addr bool) (arg, error) {
	if x.Type == nil {
		return arg{}, lw.errf(x, "a composite literal names its type")
	}
	path, ok := exprPath(x.Type)
	if !ok {
		return arg{}, lw.errf(x.Type, "a composite literal names its type as a dotted path")
	}
	a := arg{kind: argStruct, path: path, addr: addr}
	for _, el := range x.Elts {
		var e structElem
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			id, ok := kv.Key.(*ast.Ident)
			if !ok {
				return arg{}, lw.errf(kv.Key, "an element key is a field name")
			}
			e.name = id.Name
			el = kv.Value
		}
		v, err := lw.arg(el)
		if err != nil {
			return arg{}, err
		}
		e.val = v
		a.elems = append(a.elems, e)
	}
	return a, nil
}

// basicLit lowers Go's literal spellings through strconv: strings and
// rune literals unquote, ints and floats parse in any Go base.
func (lw *lowering) basicLit(x *ast.BasicLit) (arg, error) {
	switch x.Kind {
	case token.STRING, token.CHAR:
		s, err := strconv.Unquote(x.Value)
		if err != nil {
			return arg{}, lw.errf(x, "bad string %s", x.Value)
		}
		return arg{kind: argString, str: s}, nil
	case token.INT:
		i, err := strconv.ParseInt(x.Value, 0, 64)
		if err != nil {
			return arg{}, lw.errf(x, "bad int %q", x.Value)
		}
		return arg{kind: argInt, i: i}, nil
	case token.FLOAT:
		f, err := strconv.ParseFloat(x.Value, 64)
		if err != nil {
			return arg{}, lw.errf(x, "bad float %q", x.Value)
		}
		return arg{kind: argFloat, f: f}, nil
	}
	return arg{}, lw.errf(x, "%s is not in the argument grammar", x.Value)
}

// exprPath reads an ident or a selector chain of idents as the dotted
// path the grammar spells, rec.Body.String.
func exprPath(e ast.Expr) ([]string, bool) {
	var tail []string
	for {
		switch x := e.(type) {
		case *ast.Ident:
			out := make([]string, 0, len(tail)+1)
			out = append(out, x.Name)
			for i := len(tail) - 1; i >= 0; i-- {
				out = append(out, tail[i])
			}
			return out, true
		case *ast.SelectorExpr:
			tail = append(tail, x.Sel.Name)
			e = x.X
		default:
			return nil, false
		}
	}
}

// stmtName names a Go statement outside the grammar for its
// rejection message.
func stmtName(st ast.Stmt) string {
	switch st.(type) {
	case *ast.IfStmt:
		return "an if statement"
	case *ast.ForStmt:
		return "a for loop"
	case *ast.RangeStmt:
		return "a range loop"
	case *ast.SwitchStmt, *ast.TypeSwitchStmt:
		return "a switch"
	case *ast.SelectStmt:
		return "a select"
	case *ast.GoStmt:
		return "a go statement"
	case *ast.DeferStmt:
		return "a defer statement"
	case *ast.BlockStmt:
		return "a block"
	case *ast.BranchStmt:
		return "a branch statement (break, continue, goto)"
	case *ast.LabeledStmt:
		return "a label"
	case *ast.IncDecStmt:
		return "an increment statement"
	}
	return fmt.Sprintf("%T", st)
}
