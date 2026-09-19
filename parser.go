package gozero

import (
	"fmt"
)

// The grammar has no operators: a statement is a call, and every value
// is a literal, a name, or the result of another call.
//
//	program := { stmt }
//	stmt    := "var" name typeref term
//	         | "return" [ arg ] term
//	         | "for" [ name [ "," name ] ":=" ] "range" arg block term
//	         | "break" term
//	         | "continue" term
//	         | path "<-" arg term
//	         | [ name { "," name } ( ":=" | "=" ) ] rhs term
//	block   := "{" { stmt } "}"
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
// and numbers map to int64 without a decimal point and float64 with
// one; no other numeric types exist.
type Parser struct {
	src string
	pos int
	// nl records that skipping whitespace crossed a newline since the
	// last token byte was consumed, which is what lets the end of a
	// line close a statement the way a semicolon does.
	nl bool
	// hdr marks a range header, where a brace opens the body rather
	// than a composite literal; parentheses lift the restriction.
	hdr bool
	// depth counts enclosing range bodies, so a statement kind that a
	// body cannot hold is rejected where it is written.
	depth int
}

// terminated consumes a statement end. The semicolon is a delimiter
// between statements sharing a line, not something every line has to
// carry: the end of the line and the end of the source both close a
// statement, and so does a closing brace, which is Go's rule that a
// semicolon may be omitted before "}".
func (p *Parser) terminated() bool {
	if p.consume(';') {
		return true
	}
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == '}' {
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

	// rng is a range loop, "for x := range xs { ... }".
	rng *rangeStmt

	// brk and cont are break and continue, which only stand inside a
	// range body: the parser rejects them anywhere else, which is what
	// keeps their control signals from escaping a loop.
	brk  bool
	cont bool
}

// program is a parsed source unit.
type program struct {
	stmts []stmt
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
	for _, a := range s.call.args {
		if a.kind == argCall || a.kind == argStruct {
			return nil, false
		}
	}
	return s.call, true
}

// Parse parses a program.
func (p *Parser) Parse(src string) (*program, error) {
	p.src, p.pos = src, 0

	prog := &program{}
	for {
		p.skipSpace()
		if p.pos >= len(p.src) {
			break
		}
		s, err := p.stmt()
		if err != nil {
			return nil, err
		}
		prog.stmts = append(prog.stmts, s)
	}
	if len(prog.stmts) == 0 {
		return nil, fmt.Errorf("parse: empty program")
	}
	return prog, nil
}

func (p *Parser) stmt() (stmt, error) {
	if p.keyword("var") {
		if p.depth > 0 {
			return stmt{}, fmt.Errorf("parse: a var declaration cannot stand inside a range body, declare the name before the loop (offset %d)", p.pos)
		}
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
		if p.depth > 0 {
			return stmt{}, fmt.Errorf("parse: return cannot stand inside a range body, a body runs every statement of every iteration (offset %d)", p.pos)
		}
		s := stmt{ret: true}
		p.skipSpace()
		if p.terminated() {
			return s, nil
		}
		// The call form is tried first so "return f(x);" parses its
		// path once; only when that fails is the value form read.
		save := p.pos
		if call, err := p.expr(); err == nil {
			s.call = call
		} else {
			p.pos = save
			a, err := p.arg()
			if err != nil {
				return s, err
			}
			if a.kind == argCall {
				s.call = a.sub
			} else {
				s.retVal = &a
			}
		}
		if !p.terminated() {
			return s, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
		}
		return s, nil
	}
	if p.keyword("for") {
		return p.forRange()
	}
	// break and continue only stand inside a range body, which is the
	// guarantee that lets their signals travel the error return: a
	// loop always encloses them, so a signal never reaches a caller.
	if p.keyword("break") {
		return p.loopExit("break", func() stmt { return stmt{brk: true} })
	}
	if p.keyword("continue") {
		return p.loopExit("continue", func() stmt { return stmt{cont: true} })
	}

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
				a, err := p.arg()
				if err != nil {
					return stmt{}, err
				}
				if a.kind == argVar || a.kind == argPath {
					return stmt{}, fmt.Errorf("parse: cannot assign a name to a field at offset %d", p.pos)
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
			v, err := p.arg()
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
		a, err := p.arg()
		if err != nil {
			return stmt{}, err
		}
		switch a.kind {
		case argCall:
			p.pos = save
		case argVar, argPath:
			return stmt{}, fmt.Errorf("parse: cannot assign a name to a name at offset %d", save)
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
