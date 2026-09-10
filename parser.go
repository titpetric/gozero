package gozero

import (
	"fmt"
)

// The statement grammar below is the language's core: a statement is
// a call, and a value is a literal, a name, or the result of another
// call. Layered over it are the Go-subset extensions, each in its
// own file: operator expressions (parser_expr.go), blocks and
// control flow (parser_block.go), type declarations and the file
// header (parser_decl.go), and functions (parser_func.go);
// docs/packages.md records the adopted surface.
//
//	program := { stmt }
//	stmt    := "var" name typeref term
//	         | "return" [ arg ] term
//	         | path "<-" arg term
//	         | [ name { "," name } ( ":=" | "=" ) ] rhs term
//	term    := ";" | EOL | EOF
//	rhs     := expr | string | number | "true" | "false" | "nil" | composite | recv
//	typeref := { "*" | "[]" | "chan" | "chan<-" | "<-chan" } path
//	expr    := path "(" [ args ] ")" { "." ident "(" [ args ] ")" }
//	path    := ident { "." ident }
//	args    := arg { "," arg }
//	arg     := string | number | path | expr | composite | recv
//	recv    := "<-" ( path | expr )
//	composite := [ "&" ] path "{" [ elem { "," elem } [ "," ] ] "}"
//	elem    := [ ident ":" ] arg

// Parser turns a program into a list of statements. A path is resolved
// by the compiler, not here: http.NewRequest is one bound name,
// req.Cookies is a method on the value held by req, and req.Header is
// a struct field on it; the parser cannot tell the three apart without
// the bindings and the types. Strings are single- or double-quoted,
// or backquoted raw literals without escapes, and numbers map to
// int64 without a decimal point and float64 with one; no other
// numeric types exist.
type Parser struct {
	src string
	pos int
	// nl records that skipping whitespace crossed a newline since the
	// last token byte was consumed, which is what lets the end of a
	// line close a statement the way a semicolon does.
	nl bool
	// badComment is the offset of an unterminated block comment, -1
	// when there is none. skipSpace records it; Parse surfaces it.
	badComment int
	// hdr is set while an if or for header parses: a brace there
	// opens the block, never a composite literal, as in Go.
	hdr bool
}

// terminated consumes a statement end. The semicolon is a delimiter
// between statements sharing a line, not something every line has to
// carry: the end of the line and the end of the source both close a
// statement.
func (p *Parser) terminated() bool {
	if p.consume(';') {
		return true
	}
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == '}' {
		// The closing brace of a block ends the statement before it,
		// Go's inserted semicolon; the block loop consumes it.
		return true
	}
	return p.pos >= len(p.src) || p.nl
}

type argKind int

const (
	argString argKind = iota
	argInt
	argFloat
	argVar
	argBool
	// argNil is the nil literal: the zero value of whatever nilable
	// parameter it fills.
	argNil
	argCall
	// argPath is a dotted name with no call after it, "req.Header". The
	// first segment is a name and the rest are field selectors; the
	// compiler resolves them, because only it knows the types.
	argPath
	// argStruct is a composite literal, url.URL{Path: "/"} or
	// &http.Request{}. The path names the type; the compiler resolves
	// it, because only it holds the registry.
	argStruct
	// argRecv is a channel receive, <-c. The source is a name, a
	// field, or a call; the compiler types it.
	argRecv
	// argBinary is op applied to x and y; argUnary is op applied to
	// x. Short-circuiting and typing are the compiler's.
	argBinary
	argUnary
	// argIndex is x[y].
	argIndex
	// argFuncLit is a func literal: a value that captures what it
	// reads from enclosing scopes.
	argFuncLit
)

// structElem is one element of a composite literal: the field name
// when the element is keyed, empty when it is positional, and the
// value.
type structElem struct {
	name string
	val  arg
}

// arg is one parsed argument: a literal, a name, or a nested call.
type arg struct {
	kind argKind
	str  string   // argString value or argVar name
	path []string // argPath segments
	i    int64
	f    float64
	b    bool      // argBool
	sub  *callExpr // argCall
	// spread marks "xs...": the value expands into a variadic
	// parameter.
	spread bool
	// argStruct: path names the type, elems are the elements, and addr
	// marks the &T{} form.
	elems []structElem
	addr  bool
	// argRecv: the channel the receive reads.
	recv *arg
	// argBinary, argUnary, argIndex: the operator and its operands.
	op string
	x  *arg
	y  *arg
	// argFuncLit: the literal's parameters, results and body.
	fn *funcDecl
}

// link is one ".Method(args)" step chained onto a call.
type link struct {
	name string
	args []arg
}

// callExpr is a call and the chain of method calls applied to its
// result. path holds the dotted name written in the source, split on
// the dots.
type callExpr struct {
	path  []string
	args  []arg
	chain []link
}

// stmt is one statement of a program. Exactly one of call, lit and
// varType describes what it does; a bare "return;" has none of them.
type stmt struct {
	lhs    []string // names bound to the results, empty to discard
	define bool     // ":=" rather than "="
	ret    bool     // a return statement
	call   *callExpr

	// lit is set when the right-hand side is a literal rather than a
	// call, "x = 123". There is no call to take a type from, so the
	// compiler infers one.
	lit *arg

	// varName and varType are set by a var statement, which puts the
	// zero value of a named type in scope.
	varName string
	varType string

	// retVal is a return statement's value when it is not a call:
	// "return x;", "return req.Header;", "return 5;".
	retVal *arg

	// fieldLhs is the dotted target of a field assignment,
	// "req.Method = ...". The base is a program-bound name and the
	// rest are field selectors.
	fieldLhs []string

	// sendCh and sendVal are a send statement, "c <- v". The path
	// names the channel the way fieldLhs names a field target.
	sendCh  []string
	sendVal *arg

	// Control flow: an if chain, a loop, or the loop-only jumps.
	ifs  *ifStmt
	fors *forStmt
	brk  bool
	cont bool

	// deferred marks "defer call()": the call's arguments evaluate
	// here, the call itself runs when the program exits.
	deferred bool

	// rets is a multi-value return inside a function body.
	rets []arg
}

// program is a parsed source unit.
type program struct {
	stmts []stmt
	// types are the program's own type declarations, collected apart
	// from the statements: a declaration compiles to a reflect type,
	// not to anything that runs.
	types []typeDecl
	// pkg and imports are the file header. pkg is empty for a snippet;
	// a file resolves names only through its import block.
	pkg     string
	imports []importSpec
	// funcs are the function and method declarations. In a file they
	// are the program; a snippet may declare them before its
	// statements.
	funcs []funcDecl
}

// flatCall reports the single call of a one-statement program whose
// arguments are all leaves, and whether the program has that shape.
// This is the form the JIT shape table matches, and the form every
// statement had before programs grew past one line.
func (p *program) flatCall() (*callExpr, bool) {
	if len(p.stmts) != 1 {
		return nil, false
	}
	s := p.stmts[0]
	if !s.ret || s.call == nil || len(s.call.chain) != 0 || len(s.call.path) != 1 {
		return nil, false
	}
	// An allow list of exactly the leaf kinds compileStatement
	// handles, rather than a list of exclusions, so a new argument
	// kind can never leak into the single-statement shape table by
	// omission.
	for _, a := range s.call.args {
		switch a.kind {
		case argString, argInt, argFloat, argVar:
		default:
			return nil, false
		}
	}
	return s.call, true
}

// Parse parses a program.
func (p *Parser) Parse(src string) (*program, error) {
	p.src, p.pos, p.badComment = src, 0, -1

	prog := &program{}
	file, err := p.fileHeader(prog)
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if p.badComment >= 0 {
			return nil, p.errAt(p.badComment, "unterminated comment")
		}
		if p.pos >= len(p.src) {
			break
		}
		if td, ok, err := p.typeDeclSniff(); err != nil {
			return nil, err
		} else if ok {
			prog.types = append(prog.types, td)
			continue
		}
		if fd, ok, err := p.funcDeclSniff(); err != nil {
			return nil, err
		} else if ok {
			prog.funcs = append(prog.funcs, fd)
			continue
		}
		if file {
			// A file holds declarations, as in Go; statements are the
			// snippet form.
			return nil, p.errAt(p.pos, "expected a declaration")
		}
		s, err := p.stmt()
		if err != nil {
			return nil, err
		}
		prog.stmts = append(prog.stmts, s)
	}
	if len(prog.stmts) == 0 && !file && len(prog.funcs) == 0 {
		return nil, fmt.Errorf("parse: empty program")
	}
	return prog, nil
}

func (p *Parser) stmt() (stmt, error) {
	if p.keyword("var") {
		name := p.ident()
		if name == "" {
			return stmt{}, fmt.Errorf("parse: expected a name after var at offset %d", p.pos)
		}
		typ, err := p.typeRef()
		if err != nil {
			return stmt{}, err
		}
		if !p.terminated() {
			return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
		}
		return stmt{varName: name, varType: typ}, nil
	}
	if p.keyword("return") {
		s := stmt{ret: true}
		p.skipSpace()
		if p.terminated() {
			return s, nil
		}
		// The call form is tried first so "return f(x);" parses its
		// path once, but only kept when the statement ends there:
		// "return f(x) + 1" reparses as an expression.
		save, saveNL := p.pos, p.nl
		if call, err := p.expr(); err == nil && p.terminated() {
			s.call = call
			return s, nil
		}
		p.pos, p.nl = save, saveNL
		a, err := p.exprArg()
		if err != nil {
			return s, err
		}
		if p.peek() == ',' {
			// A multi-value return, legal inside a function body; the
			// compiler checks the arity against the declaration.
			s.rets = append(s.rets, a)
			for p.consume(',') {
				next, err := p.exprArg()
				if err != nil {
					return s, err
				}
				s.rets = append(s.rets, next)
			}
		} else if a.kind == argCall {
			s.call = a.sub
		} else {
			s.retVal = &a
		}
		if !p.terminated() {
			return s, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
		}
		return s, nil
	}

	if p.keyword("if") {
		return p.parseIf()
	}
	deferSave := p.pos
	if p.keyword("defer") {
		if call, err := p.expr(); err == nil && p.terminated() {
			return stmt{call: call, deferred: true}, nil
		}
		p.pos = deferSave
	}
	if p.keyword("for") {
		return p.parseFor()
	}
	jumpSave := p.pos
	if p.keyword("break") {
		if p.terminated() {
			return stmt{brk: true}, nil
		}
		p.pos = jumpSave
	}
	if p.keyword("continue") {
		if p.terminated() {
			return stmt{cont: true}, nil
		}
		p.pos = jumpSave
	}

	// path++ and path-- desugar to the assignment forms.
	incSave, incNL := p.pos, p.nl
	if p.ident() != "" {
		p.pos, p.nl = incSave, incNL
		path, _ := p.path()
		if inc, ok := p.incDec(path); ok {
			if !p.terminated() {
				return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
			}
			return inc, nil
		}
	}
	p.pos, p.nl = incSave, incNL

	// A dotted path followed by a single "=" is a field assignment.
	// It is sniffed before the assignment list, which only reads bare
	// names; a path followed by "(" is a call and rewinds.
	fieldSave := p.pos
	if p.ident() != "" && p.peek() == '.' {
		p.pos = fieldSave
		path, _ := p.path()
		if len(path) >= 2 {
			p.skipSpace()
			if p.pos < len(p.src) && p.src[p.pos] == '=' && (p.pos+1 >= len(p.src) || p.src[p.pos+1] != '=') {
				p.pos++
				a, err := p.exprArg()
				if err != nil {
					return stmt{}, err
				}
				if !p.terminated() {
					return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
				}
				if a.kind == argCall {
					return stmt{fieldLhs: path, call: a.sub}, nil
				}
				return stmt{fieldLhs: path, lit: &a}, nil
			}
		}
	}
	p.pos = fieldSave

	// A path followed by "<-" is a send. Nothing else in the grammar
	// puts "<-" after a path, so the sniff cannot misread a call or an
	// assignment.
	sendSave := p.pos
	if p.ident() != "" {
		p.pos = sendSave
		path, _ := p.path()
		p.skipSpace()
		if p.consumeStr("<-") {
			v, err := p.exprArg()
			if err != nil {
				return stmt{}, err
			}
			if !p.terminated() {
				return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
			}
			return stmt{sendCh: path, sendVal: &v}, nil
		}
	}
	p.pos = sendSave

	// A bare receive, "<-c;", runs for its blocking effect.
	if p.pos+1 < len(p.src) && p.src[p.pos] == '<' && p.src[p.pos+1] == '-' {
		a, err := p.arg()
		if err != nil {
			return stmt{}, err
		}
		if !p.terminated() {
			return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
		}
		return stmt{lit: &a}, nil
	}

	// A statement is a call, optionally preceded by the names its
	// results bind to. The names are only known to be names once the
	// assignment operator is seen, so the position is saved and the
	// scan restarts as a bare call when it is not.
	save := p.pos
	lhs, define, ok := p.assignList()
	if !ok {
		p.pos = save
		lhs, define = nil, false
	}

	// The right-hand side is one arg: a call is the statement, a
	// literal assigns, and a bare name is rejected here with its own
	// message rather than surfacing as "expected '('".
	if len(lhs) > 0 {
		save := p.pos
		a, err := p.exprArg()
		if err != nil {
			return stmt{}, err
		}
		blankOnly := true
		for _, n := range lhs {
			if n != "_" {
				blankOnly = false
				break
			}
		}
		switch a.kind {
		case argCall:
			p.pos = save
		case argVar, argPath:
			// _ = n is Go's discard; anything else on the left reads
			// as a copy the language does not have.
			if !blankOnly {
				return stmt{}, fmt.Errorf("parse: cannot assign a name to a name at offset %d", save)
			}
			if !p.terminated() {
				return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
			}
			return stmt{lhs: lhs, define: define, lit: &a}, nil
		default:
			if !p.terminated() {
				return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
			}
			return stmt{lhs: lhs, define: define, lit: &a}, nil
		}
	}

	call, err := p.expr()
	if err != nil {
		return stmt{}, err
	}
	if !p.terminated() {
		return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
	}
	return stmt{lhs: lhs, define: define, call: call}, nil
}

// assignList scans "a, b :=" or "a =" and reports whether one was
// there. It never returns an error: anything that does not match is the
// caller's cue to rewind and read a bare call.
func (p *Parser) assignList() ([]string, bool, bool) {
	var lhs []string
	for {
		name := p.ident()
		if name == "" {
			return nil, false, false
		}
		lhs = append(lhs, name)
		p.skipSpace()
		if p.consume(',') {
			continue
		}
		break
	}
	if p.consumeStr(":=") {
		return lhs, true, true
	}
	// "==" does not exist in the grammar, but a lone "=" must not
	// swallow one if it ever does.
	if p.pos < len(p.src) && p.src[p.pos] == '=' && (p.pos+1 >= len(p.src) || p.src[p.pos+1] != '=') {
		p.pos++
		return lhs, false, true
	}
	return nil, false, false
}
