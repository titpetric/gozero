package gozero

import (
	"fmt"
)

// The two loop forms beside range, both header-scoped. A condition
// loop's condition is a bool name, field or call, read once per
// iteration; the three-clause header admits exactly an init
// assignment, one comparison, and an increment or decrement of the
// loop variable. Comparisons and the ++/-- forms exist only in this
// header: neither is an expression the grammar has anywhere else, so
// the operator-free rule for statements and arguments stands.

// forStmt is a parsed condition or three-clause loop. cond is set on
// the condition form and nil on the other, where the clause fields
// carry the header.
type forStmt struct {
	cond *arg // a bool name, field or call

	initName string
	initVal  *arg
	cmpOp    string
	cmpX     *arg
	cmpY     *arg
	postDec  bool // i-- rather than i++

	body []stmt
}

// forLoop dispatches the loop forms after the for keyword: range with
// or without names, the three-clause header, and the condition loop.
func (p *Parser) forLoop() (stmt, error) {
	save, saveNL := p.pos, p.nl
	if p.keyword("range") {
		p.pos, p.nl = save, saveNL
		return p.forRange()
	}
	if lhs, define, ok := p.assignList(); ok {
		rangeSave, rangeNL := p.pos, p.nl
		if p.keyword("range") {
			// The range reader owns its whole header; rewind so it
			// re-reads the names it checks itself.
			p.pos, p.nl = save, saveNL
			return p.forRange()
		}
		p.pos, p.nl = rangeSave, rangeNL
		if !define {
			return stmt{}, fmt.Errorf("parse: a three-clause init declares its name with := at offset %d", p.pos)
		}
		if len(lhs) != 1 {
			return stmt{}, fmt.Errorf("parse: a three-clause init declares exactly one name at offset %d", p.pos)
		}
		return p.forClauses(lhs[0])
	}
	p.pos, p.nl = save, saveNL
	return p.forCond()
}

// forCond reads a condition loop: a bool name, field or call, then
// the body. The bare and constant forms are rejected by name: a loop
// whose condition nothing can change has no bound but the context.
func (p *Parser) forCond() (stmt, error) {
	if p.peek() == '{' {
		return stmt{}, fmt.Errorf("parse: a bare for has no bound and is not in the language; loop over a range or a bool condition (offset %d)", p.pos)
	}
	saved := p.hdr
	p.hdr = true
	cond, err := p.arg()
	p.hdr = saved
	if err != nil {
		return stmt{}, err
	}
	switch cond.kind {
	case argVar, argPath, argCall:
	case argBool:
		return stmt{}, fmt.Errorf("parse: a constant condition has no bound; a loop condition is a bool name, field or call (offset %d)", p.pos)
	default:
		return stmt{}, fmt.Errorf("parse: a loop condition is a bool name, field or call (offset %d)", p.pos)
	}
	if c := p.peek(); c == '<' || c == '>' || c == '=' || c == '!' {
		return stmt{}, fmt.Errorf("parse: a comparison stands only in the three-clause for header (offset %d)", p.pos)
	}
	f := &forStmt{cond: &cond}
	body, err := p.loopBody()
	if err != nil {
		return stmt{}, err
	}
	f.body = body
	if !p.terminated() {
		return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
	}
	return stmt{fors: f}, nil
}

// forClauses reads the rest of a three-clause header after "name :=":
// the init value, the comparison, the post clause, and the body. All
// three clauses are required; Go's forms with a clause omitted are
// not in the language.
func (p *Parser) forClauses(name string) (stmt, error) {
	f := &forStmt{initName: name}
	saved := p.hdr
	p.hdr = true

	iv, err := p.arg()
	if err != nil {
		p.hdr = saved
		return stmt{}, err
	}
	f.initVal = &iv
	if !p.consume(';') {
		p.hdr = saved
		return stmt{}, fmt.Errorf("parse: expected ';' after the init clause at offset %d", p.pos)
	}

	if p.peek() == ';' {
		p.hdr = saved
		return stmt{}, fmt.Errorf("parse: a three-clause for takes all three clauses; a form with one omitted is not in the language (offset %d)", p.pos)
	}
	x, err := p.arg()
	if err != nil {
		p.hdr = saved
		return stmt{}, err
	}
	op, ok := p.relop()
	if !ok {
		p.hdr = saved
		return stmt{}, fmt.Errorf("parse: a three-clause for takes one comparison: ==, !=, <, <=, > or >= (offset %d)", p.pos)
	}
	y, err := p.arg()
	if err != nil {
		p.hdr = saved
		return stmt{}, err
	}
	f.cmpX, f.cmpOp, f.cmpY = &x, op, &y
	if !p.consume(';') {
		p.hdr = saved
		return stmt{}, fmt.Errorf("parse: expected ';' after the comparison at offset %d", p.pos)
	}

	post := p.ident()
	switch {
	case post == "":
		p.hdr = saved
		return stmt{}, fmt.Errorf("parse: the post clause is %s++ or %s-- (offset %d)", name, name, p.pos)
	case post != name:
		p.hdr = saved
		return stmt{}, fmt.Errorf("parse: the post clause increments the loop variable %s, not %s (offset %d)", name, post, p.pos)
	}
	switch {
	case p.consumeStr("++"):
	case p.consumeStr("--"):
		f.postDec = true
	default:
		p.hdr = saved
		return stmt{}, fmt.Errorf("parse: the post clause is %s++ or %s-- (offset %d)", name, name, p.pos)
	}
	p.hdr = saved

	body, err := p.loopBody()
	if err != nil {
		return stmt{}, err
	}
	f.body = body
	if !p.terminated() {
		return stmt{}, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
	}
	return stmt{fors: f}, nil
}

// relop reads one comparison operator, two-byte forms first so <= is
// not read as < followed by a stray =.
func (p *Parser) relop() (string, bool) {
	for _, op := range [...]string{"==", "!=", "<=", ">=", "<", ">"} {
		if p.consumeStr(op) {
			return op, true
		}
	}
	return "", false
}

// loopBody reads a braced statement list. depth is what admits break
// and continue and rejects var and return, for every loop form alike.
func (p *Parser) loopBody() ([]stmt, error) {
	if !p.consume('{') {
		return nil, fmt.Errorf("parse: expected '{' after the loop header at offset %d", p.pos)
	}
	p.depth++
	defer func() { p.depth-- }()
	var body []stmt
	for {
		p.skipSpace()
		if p.consume('}') {
			return body, nil
		}
		if p.pos >= len(p.src) {
			return nil, fmt.Errorf("parse: unterminated loop body at offset %d", p.pos)
		}
		s, err := p.stmt()
		if err != nil {
			return nil, err
		}
		body = append(body, s)
	}
}
