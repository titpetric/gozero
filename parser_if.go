package gozero

import (
	"fmt"
)

// Conditions, the L1 to L3 rungs: if / else if / else over braced
// statement lists. The header is an expression under Go precedence:
// || over && over one comparison over + - over * / %, with unary !
// and parentheses; the leaves are names, field paths, calls and
// literals, and there is still no init clause. The operators exist
// only here; every other position rejects them by name.
// docs/design/conditions.md records what the fuller forms cost.
//
//	ifstmt  := "if" cond block [ "else" ( ifstmt | block ) ]
//	cond    := orexpr
//	orexpr  := andexpr { "||" andexpr }
//	andexpr := cmpexpr { "&&" cmpexpr }
//	cmpexpr := add [ cmpop add ]
//	add     := mul { ( "+" | "-" ) mul }
//	mul     := unary { ( "*" | "/" | "%" ) unary }
//	unary   := "!" unary | primary
//	primary := "(" cond ")" | operand
//	operand := path | expr | string | number
//	cmpop   := "==" | "!=" | "<" | "<=" | ">" | ">="
//	block   := "{" { stmt } "}"

// ifStmt is one if with its else chain. An else-if nests: els holds
// a single statement that is itself an if.
type ifStmt struct {
	cond *condExpr
	then []stmt
	els  []stmt
}

// condExpr is one node of an if-header expression: a leaf operand,
// or an operator over one or two subtrees. The tree exists only
// under an if header; the statement grammar rejects every operator
// by name.
type condExpr struct {
	op   string    // "" for a leaf; "!" carries x only
	x, y *condExpr // operator arms
	leaf arg       // the operand when op is ""
	lit  bool      // the leaf is a literal, never a whole condition
}

// parseIf reads an if statement after the keyword.
func (p *Parser) parseIf() (stmt, error) {
	is := &ifStmt{}
	cond, err := p.cond()
	if err != nil {
		return stmt{}, err
	}
	is.cond = cond
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

// cond reads an if condition: the header expression, precedence
// climbing over the operand forms. A bare literal is caught here: it
// can only be an operator's side, never the whole condition.
func (p *Parser) cond() (*condExpr, error) {
	at := p.pos
	ce, err := p.hdrExpr(1)
	if err != nil {
		return nil, err
	}
	if ce.op == "" && ce.lit {
		return nil, fmt.Errorf("parse: a literal is not an if condition at offset %d", at)
	}
	return ce, nil
}

// hdrExpr parses one precedence level and everything binding
// tighter. Left-associative: a-b-c is (a-b)-c, the climb passing
// prec+1 to the right side.
func (p *Parser) hdrExpr(minPrec int) (*condExpr, error) {
	left, err := p.hdrUnary()
	if err != nil {
		return nil, err
	}
	for {
		op, prec := p.hdrOp()
		if op == "" || prec < minPrec {
			return left, nil
		}
		p.pos += len(op)
		p.nl = false
		right, err := p.hdrExpr(prec + 1)
		if err != nil {
			return nil, err
		}
		left = &condExpr{op: op, x: left, y: right}
	}
}

// hdrUnary reads the ! prefix. A minus straight onto a digit stays
// the negative literal the operand reads, so -1 is a constant, not
// an operation; there is no unary minus over names in this rung.
func (p *Parser) hdrUnary() (*condExpr, error) {
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == '!' && (p.pos+1 >= len(p.src) || p.src[p.pos+1] != '=') {
		p.pos++
		p.nl = false
		x, err := p.hdrUnary()
		if err != nil {
			return nil, err
		}
		return &condExpr{op: "!", x: x}, nil
	}
	return p.hdrPrimary()
}

// hdrPrimary reads a parenthesized subexpression or one operand.
func (p *Parser) hdrPrimary() (*condExpr, error) {
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == '(' {
		p.pos++
		p.nl = false
		ce, err := p.hdrExpr(1)
		if err != nil {
			return nil, err
		}
		if !p.consume(')') {
			return nil, fmt.Errorf("parse: expected ')' in the if header at offset %d", p.pos)
		}
		return ce, nil
	}
	a, lit, err := p.condOperand()
	if err != nil {
		return nil, err
	}
	return &condExpr{leaf: a, lit: lit}, nil
}

// hdrOp reports the binary operator at the cursor and its Go
// precedence, consuming nothing. An operator never starts a line,
// Go's own semicolon rule, so continuation is spelled with the
// operator trailing; '<' straight onto '-' is the channel arrow,
// never a comparison, matching Go's tokenizer.
func (p *Parser) hdrOp() (string, int) {
	p.skipSpace()
	if p.nl || p.pos >= len(p.src) {
		return "", 0
	}
	if p.pos+1 < len(p.src) {
		switch p.src[p.pos : p.pos+2] {
		case "||":
			return "||", 1
		case "&&":
			return "&&", 2
		case "==":
			return "==", 3
		case "!=":
			return "!=", 3
		case "<=":
			return "<=", 3
		case ">=":
			return ">=", 3
		case "<-":
			return "", 0
		}
	}
	switch p.src[p.pos] {
	case '<':
		return "<", 3
	case '>':
		return ">", 3
	case '+':
		return "+", 4
	case '-':
		return "-", 4
	case '*':
		return "*", 5
	case '/':
		return "/", 5
	case '%':
		return "%", 5
	}
	return "", 0
}

// condOperand reads one condition operand: a literal, a call, or a
// dotted path. It is not p.arg on purpose: a path followed by '{'
// must open the block rather than a composite literal, Go's own rule
// for an if header. lit reports a literal, which cannot stand alone
// as the condition.
func (p *Parser) condOperand() (a arg, lit bool, err error) {
	if c := p.peek(); c == '-' || c == '"' || c == '\'' || (c >= '0' && c <= '9') {
		a, err = p.arg()
		return a, true, err
	}
	save := p.pos
	if call, err := p.expr(); err == nil {
		return arg{kind: argCall, sub: call}, false, nil
	}
	p.pos = save
	path, err := p.path()
	if err != nil {
		return arg{}, false, fmt.Errorf("parse: expected an if condition at offset %d", p.pos)
	}
	if len(path) == 1 {
		return arg{kind: argVar, str: path[0]}, false, nil
	}
	return arg{kind: argPath, path: path}, false, nil
}

// cmpOp reads a comparison operator, longest spelling first. '<'
// immediately followed by '-' is the channel arrow, never a
// comparison, matching Go's own tokenizer.
func (p *Parser) cmpOp() (string, bool) {
	if p.consumeStr("==") {
		return "==", true
	}
	if p.consumeStr("!=") {
		return "!=", true
	}
	if p.consumeStr("<=") {
		return "<=", true
	}
	if p.consumeStr(">=") {
		return ">=", true
	}
	p.skipSpace()
	if p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '<':
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == '-' {
				return "", false
			}
			p.pos++
			p.nl = false
			return "<", true
		case '>':
			p.pos++
			p.nl = false
			return ">", true
		}
	}
	return "", false
}

// rejectCmp fails with the placement rule when a header operator
// follows: a comparison names its own rule, and the composition and
// arithmetic operators name operator placement, so "y := x + 5"
// says what is illegal instead of surfacing as a strange assignment.
func (p *Parser) rejectCmp() error {
	save, saveNL := p.pos, p.nl
	if _, ok := p.cmpOp(); ok {
		return fmt.Errorf("parse: a comparison is only legal in an if header (comparison placement) at offset %d", save)
	}
	p.pos, p.nl = save, saveNL
	if op, prec := p.hdrOp(); op != "" && prec != 3 {
		p.pos, p.nl = save, saveNL
		return fmt.Errorf("parse: %s is only legal in an if header (operator placement) at offset %d", op, save)
	}
	p.pos, p.nl = save, saveNL
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
