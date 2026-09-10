package gozero

// Blocks and control flow:
//
//	ifstmt  := "if" expr blockst [ "else" ( "if" ifstmt | blockst ) ]
//	forstmt := "for" blockst
//	         | "for" expr blockst
//	         | "for" [ clause ] ";" [ expr ] ";" [ clause ] blockst
//	         | "for" [ name [ "," name ] ":=" ] "range" expr blockst
//	clause  := path ( "++" | "--" )
//	         | name { "," name } ( ":=" | "=" ) expr
//	         | expr-with-call
//	blockst := "{" { stmt } "}"
//
// A composite literal cannot stand bare in an if or for header, as
// in Go: the brace belongs to the block. Inside parentheses and call
// arguments the restriction lifts. else must follow the closing
// brace on the same line. i++ and i-- desugar in the parser to
// i = i + 1 and i = i - 1, so the compiler sees only assignments.

// block is a braced statement list.
type block struct {
	stmts []stmt
}

// ifStmt is one if with its else chain: else-if nests as a block
// holding a single if statement.
type ifStmt struct {
	init *stmt
	cond arg
	then block
	els  *block
}

// forStmt covers Go's three loop forms and range. rangeX non-nil
// marks a range loop with key and val names, "_" or "" for blanks;
// cond nil on a bare loop.
type forStmt struct {
	init   *stmt
	cond   *arg
	post   *stmt
	key    string
	val    string
	rangeX *arg
	body   block
}

// headerExpr reads an if or for condition: an expression with bare
// composite literals held back so the brace opens the block.
func (p *Parser) headerExpr() (arg, error) {
	saved := p.hdr
	p.hdr = true
	a, err := p.exprArg()
	p.hdr = saved
	return a, err
}

// block reads a braced statement list.
func (p *Parser) block() (block, error) {
	var b block
	p.skipSpace()
	if !p.consume('{') {
		return b, p.errAt(p.pos, "expected '{'")
	}
	for {
		p.skipSpace()
		if p.badComment >= 0 {
			return b, p.errAt(p.badComment, "unterminated comment")
		}
		if p.consume('}') {
			return b, nil
		}
		if p.pos >= len(p.src) {
			return b, p.errAt(p.pos, "unterminated block")
		}
		s, err := p.stmt()
		if err != nil {
			return b, err
		}
		b.stmts = append(b.stmts, s)
	}
}

// parseIf reads an if statement after the keyword.
func (p *Parser) parseIf() (stmt, error) {
	is := &ifStmt{}
	save, saveNL := p.pos, p.nl
	if cl, err := p.forClause(); err == nil && p.consume(';') {
		is.init = cl
	} else {
		p.pos, p.nl = save, saveNL
	}
	cond, err := p.headerExpr()
	if err != nil {
		return stmt{}, err
	}
	is.cond = cond
	if is.then, err = p.block(); err != nil {
		return stmt{}, err
	}

	// else binds only on the same line as the closing brace, as in
	// gofmt-shaped Go.
	save, saveNL = p.pos, p.nl
	p.skipSpace()
	if !p.nl && p.keyword("else") {
		if p.keyword("if") {
			sub, err := p.parseIf()
			if err != nil {
				return stmt{}, err
			}
			is.els = &block{stmts: []stmt{sub}}
			// The nested if consumed the statement's terminator.
			return stmt{ifs: is}, nil
		} else {
			blk, err := p.block()
			if err != nil {
				return stmt{}, err
			}
			is.els = &blk
		}
	} else {
		p.pos, p.nl = save, saveNL
	}
	if !p.terminated() {
		return stmt{}, p.errAt(p.pos, "expected ';' or end of line after if")
	}
	return stmt{ifs: is}, nil
}

// parseFor reads any of the loop forms after the keyword.
func (p *Parser) parseFor() (stmt, error) {
	fs := &forStmt{}
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == '{' {
		return p.forBody(fs) // for {}
	}
	if p.keyword("range") {
		return p.forRange(fs, "", "")
	}

	// k [, v] := range xs
	save, saveNL := p.pos, p.nl
	if lhs, define, ok := p.assignList(); ok && define && len(lhs) <= 2 && p.keyword("range") {
		key, val := lhs[0], ""
		if len(lhs) == 2 {
			val = lhs[1]
		}
		return p.forRange(fs, key, val)
	}
	p.pos, p.nl = save, saveNL

	// Three-clause: something before the first ';' is the init.
	if p.pos < len(p.src) && p.src[p.pos] == ';' {
		p.pos++
		p.nl = false
		return p.forCondPost(fs)
	}
	clauseSave, clauseNL := p.pos, p.nl
	if cl, err := p.forClause(); err == nil && p.consume(';') {
		fs.init = cl
		return p.forCondPost(fs)
	}
	p.pos, p.nl = clauseSave, clauseNL

	// Condition-only.
	cond, err := p.headerExpr()
	if err != nil {
		return stmt{}, err
	}
	fs.cond = &cond
	return p.forBody(fs)
}

// forRange finishes a range loop: the ranged expression and body.
func (p *Parser) forRange(fs *forStmt, key, val string) (stmt, error) {
	x, err := p.headerExpr()
	if err != nil {
		return stmt{}, err
	}
	fs.rangeX = &x
	fs.key, fs.val = key, val
	return p.forBody(fs)
}

// forCondPost reads "[cond] ; [post]" after the init's semicolon.
func (p *Parser) forCondPost(fs *forStmt) (stmt, error) {
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] != ';' {
		cond, err := p.headerExpr()
		if err != nil {
			return stmt{}, err
		}
		fs.cond = &cond
	}
	if !p.consume(';') {
		return stmt{}, p.errAt(p.pos, "expected ';' in the for header")
	}
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] != '{' {
		post, err := p.forClause()
		if err != nil {
			return stmt{}, err
		}
		fs.post = post
	}
	return p.forBody(fs)
}

func (p *Parser) forBody(fs *forStmt) (stmt, error) {
	body, err := p.block()
	if err != nil {
		return stmt{}, err
	}
	fs.body = body
	if !p.terminated() {
		return stmt{}, p.errAt(p.pos, "expected ';' or end of line after for")
	}
	return stmt{fors: fs}, nil
}

// forClause reads one simple statement of a for header, without a
// terminator: an increment, an assignment, or a call.
func (p *Parser) forClause() (*stmt, error) {
	saved := p.hdr
	p.hdr = true
	defer func() { p.hdr = saved }()

	save, saveNL := p.pos, p.nl
	if p.ident() != "" {
		p.pos, p.nl = save, saveNL
		path, _ := p.path()
		if inc, ok := p.incDec(path); ok {
			return &inc, nil
		}
	}
	p.pos, p.nl = save, saveNL

	if lhs, define, ok := p.assignList(); ok {
		a, err := p.exprArg()
		if err != nil {
			return nil, err
		}
		if a.kind == argCall {
			return &stmt{lhs: lhs, define: define, call: a.sub}, nil
		}
		return &stmt{lhs: lhs, define: define, lit: &a}, nil
	}
	p.pos, p.nl = save, saveNL

	call, err := p.expr()
	if err != nil {
		return nil, err
	}
	return &stmt{call: call}, nil
}

// incDec desugars path++ and path-- into the assignment the compiler
// already knows, so i++ is i = i + 1 and b.N-- is b.N = b.N - 1.
func (p *Parser) incDec(path []string) (stmt, bool) {
	op := ""
	switch {
	case p.consumeStr("++"):
		op = "+"
	case p.consumeStr("--"):
		op = "-"
	default:
		return stmt{}, false
	}
	var read arg
	if len(path) == 1 {
		read = arg{kind: argVar, str: path[0]}
	} else {
		read = arg{kind: argPath, path: path}
	}
	one := arg{kind: argInt, i: 1}
	rhs := arg{kind: argBinary, op: op, x: &read, y: &one}
	if len(path) == 1 {
		return stmt{lhs: path, lit: &rhs}, true
	}
	return stmt{fieldLhs: path, lit: &rhs}, true
}
