package gozero

// Function declarations and literals:
//
//	funcdecl := "func" [ "(" param ")" ] name signature blockst
//	funclit  := "func" signature blockst
//	signature := "(" [ param { "," param } ] ")" [ results ]
//	results  := typeref | "(" [ param { "," param } ] ")"
//
// A file declares funcs at the top level; func init() runs at load.
// A func literal stands where a value does and captures the names it
// reads from enclosing scopes.

// funcDecl is one function: a declaration when name is set, a
// literal otherwise. recv is the method receiver, nil for plain
// funcs.
type funcDecl struct {
	name    string
	recv    *param
	params  []param
	results []param
	body    block
	pos     int
}

// funcDeclSniff claims "func" at declaration position: a receiver or
// a name must follow, so a func literal in an expression never
// arrives here. It rewinds on anything else.
func (p *Parser) funcDeclSniff() (funcDecl, bool, error) {
	save := p.pos
	if !p.keyword("func") {
		return funcDecl{}, false, nil
	}
	fd := funcDecl{pos: save}
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == '(' {
		// A receiver: (name Type) or (name *Type).
		p.pos++
		p.nl = false
		rname := p.ident()
		if rname == "" {
			p.pos = save
			return funcDecl{}, false, nil
		}
		rtyp, err := p.typeRef()
		if err != nil {
			return funcDecl{}, true, err
		}
		if !p.consume(')') {
			return funcDecl{}, true, p.errAt(p.pos, "expected ')' after the receiver")
		}
		fd.recv = &param{name: rname, typ: rtyp}
	}
	fd.name = p.ident()
	if fd.name == "" {
		p.pos = save
		return funcDecl{}, false, nil
	}
	if !p.consume('(') {
		return funcDecl{}, true, p.errAt(p.pos, "expected '(' after the function name")
	}
	if err := p.funcRest(&fd); err != nil {
		return funcDecl{}, true, err
	}
	return fd, true, nil
}

// funcLit reads a literal after the func keyword was consumed and
// the next byte is known to be '('.
func (p *Parser) funcLit(pos int) (arg, error) {
	fd := funcDecl{pos: pos}
	p.pos++ // the '('
	p.nl = false
	if err := p.funcRest(&fd); err != nil {
		return arg{}, err
	}
	return arg{kind: argFuncLit, fn: &fd}, nil
}

// funcRest reads params, results and the body, from just after the
// opening paren of the parameter list.
func (p *Parser) funcRest(fd *funcDecl) error {
	params, err := p.paramList()
	if err != nil {
		return err
	}
	fd.params = params
	results, err := p.resultList()
	if err != nil {
		return err
	}
	fd.results = results
	// The body brace, not a composite: clear the header flag the way
	// parens do.
	saved := p.hdr
	p.hdr = false
	body, err := p.block()
	p.hdr = saved
	if err != nil {
		return err
	}
	fd.body = body
	return nil
}
