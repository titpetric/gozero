package gozero

import (
	"fmt"
)

// The two loop forms beside range, both header-scoped. A condition
// loop's condition is a bool name, field or call, read once per
// iteration; the three-clause header admits exactly an init
// assignment, one comparison, and an increment or decrement of the
// loop variable.
//
//	forstmt := "for" operand block term
//	         | "for" name ":=" operand ";" cond ";" name ( "++" | "--" ) block term
//
// The header reads its pieces with what is already there: the
// operand and comparison readers of parser_cmp.go, the step operator
// of a step statement, and the braced statement list of parser_if.go.
// A comparison still stands only inside a header, and the post
// clause is the statement form ++ and -- already have, so nothing
// outside a for header gains an operator.

// forStmt is a parsed condition or three-clause loop. cond is set on
// the condition form and nil on the other, where the clause fields
// carry the header.
type forStmt struct {
	cond *arg // a bool name, field or call

	initName string
	initVal  arg
	cmp      *cmpExpr
	postDec  bool // i-- rather than i++

	body []stmt
}

// forLoop dispatches the three loop forms after the for keyword:
// range with or without names, the three-clause header, and the
// condition loop.
func (p *Parser) forLoop() (stmt, error) {
	save, saveNL := p.pos, p.nl
	if p.keyword("range") {
		p.pos, p.nl = save, saveNL
		return p.forRange()
	}
	if lhs, define, ok := p.assignList(); ok {
		clause, clauseNL := p.pos, p.nl
		if p.keyword("range") {
			// The range reader owns its whole header; rewind so it
			// re-reads the names it checks itself.
			p.pos, p.nl = save, saveNL
			return p.forRange()
		}
		p.pos, p.nl = clause, clauseNL
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
// the body. The bare and constant forms are rejected by name, a loop
// whose condition nothing can change has no bound but the context,
// and so is a comparison, which belongs to the three-clause header.
func (p *Parser) forCond() (stmt, error) {
	if p.peek() == '{' {
		return stmt{}, fmt.Errorf("parse: a bare for has no bound and is not in the language; loop over a range or a bool condition (offset %d)", p.pos)
	}
	cond, lit, err := p.condOperand()
	if err != nil {
		return stmt{}, err
	}
	switch {
	case lit:
		return stmt{}, fmt.Errorf("parse: a loop condition is a bool name, field or call (offset %d)", p.pos)
	case cond.kind == argVar && (cond.str == "true" || cond.str == "false"):
		return stmt{}, fmt.Errorf("parse: a constant condition has no bound; a loop condition is a bool name, field or call (offset %d)", p.pos)
	}
	if _, ok := p.cmpOp(); ok {
		return stmt{}, fmt.Errorf("parse: a comparison stands only in the three-clause for header (offset %d)", p.pos)
	}
	f := &forStmt{cond: &cond}
	return p.forBody(f)
}

// forClauses reads the rest of a three-clause header after "name :=":
// the init value, the comparison, the post clause, and the body. All
// three clauses are required; Go's forms with a clause omitted are
// not in the language.
func (p *Parser) forClauses(name string) (stmt, error) {
	f := &forStmt{initName: name}
	iv, _, err := p.condOperand()
	if err != nil {
		return stmt{}, err
	}
	f.initVal = iv
	if !p.consume(';') {
		return stmt{}, fmt.Errorf("parse: expected ';' after the init clause at offset %d", p.pos)
	}

	if p.peek() == ';' {
		return stmt{}, fmt.Errorf("parse: a three-clause for takes all three clauses; a form with one omitted is not in the language (offset %d)", p.pos)
	}
	_, cmp, err := p.cond()
	if err != nil {
		return stmt{}, err
	}
	if cmp == nil {
		return stmt{}, fmt.Errorf("parse: a three-clause for takes one comparison: ==, !=, <, <=, > or >= (offset %d)", p.pos)
	}
	f.cmp = cmp
	if !p.consume(';') {
		return stmt{}, fmt.Errorf("parse: expected ';' after the comparison at offset %d", p.pos)
	}

	if post := p.ident(); post != name {
		if post == "" {
			return stmt{}, fmt.Errorf("parse: the post clause is %s++ or %s-- (offset %d)", name, name, p.pos)
		}
		return stmt{}, fmt.Errorf("parse: the post clause steps the loop variable %s, not %s (offset %d)", name, post, p.pos)
	}
	delta := p.consumeIncDec()
	if delta == 0 {
		return stmt{}, fmt.Errorf("parse: the post clause is %s++ or %s-- (offset %d)", name, name, p.pos)
	}
	f.postDec = delta < 0
	return p.forBody(f)
}

// forBody reads the braced statement list both headers close with.
// depth is what admits break and continue and rejects var and
// return, for every loop form alike.
func (p *Parser) forBody(f *forStmt) (stmt, error) {
	p.depth++
	body, err := p.block()
	p.depth--
	if err != nil {
		return stmt{}, err
	}
	f.body = body
	if !p.terminated() {
		return stmt{}, fmt.Errorf("parse: expected ';' or end of line after the loop at offset %d", p.pos)
	}
	return stmt{fors: f}, nil
}
