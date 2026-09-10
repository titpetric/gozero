package gozero

import (
	"strings"
)

// Declarations. Cut one is type declarations:
//
//	typedecl   := "type" name ( structtype | ifacetype ) term
//	structtype := "struct" "{" { field term } "}"
//	field      := name { "," name } typeref [ tag ]
//	            | [ "*" ] path [ tag ]                  // embedded
//	ifacetype  := "interface" "{" { name signature term } "}"
//	signature  := "(" [ param { "," param } ] ")" [ results ]
//	results    := typeref | "(" [ param { "," param } ] ")"
//	param      := [ name ] typeref
//	tag        := rawstring | string
//
// Everything accepted is legal Go. The parser records the shapes; the
// compiler builds the reflect types and owns every semantic check.

// importSpec is one import: an optional local alias and the quoted
// path, which is a BindPackage registration key.
type importSpec struct {
	alias string
	path  string
}

// fileHeader claims "package name" at the top of the source and the
// import declarations after it. The sniff needs the full clause and
// rewinds otherwise, so a snippet assigning to a name called package
// keeps parsing. It reports whether the source is a file: a file
// holds declarations only, the way a Go file does.
func (p *Parser) fileHeader(prog *program) (bool, error) {
	save := p.pos
	if !p.keyword("package") {
		return false, nil
	}
	name := p.ident()
	if name == "" || !p.terminated() {
		p.pos = save
		return false, nil
	}
	prog.pkg = name
	for {
		p.skipSpace()
		if !p.keyword("import") {
			return true, nil
		}
		if err := p.importDecl(prog); err != nil {
			return true, err
		}
	}
}

// importDecl reads one import declaration after the keyword: a single
// spec or the factored block.
func (p *Parser) importDecl(prog *program) error {
	p.skipSpace()
	if p.consume('(') {
		for {
			p.skipSpace()
			if p.consume(')') {
				break
			}
			if p.pos >= len(p.src) {
				return p.errAt(p.pos, "unterminated import block")
			}
			spec, err := p.importSpec()
			if err != nil {
				return err
			}
			prog.imports = append(prog.imports, spec)
			if !p.fieldTerm() {
				return p.errAt(p.pos, "expected ';' or end of line after import")
			}
		}
		if !p.terminated() {
			return p.errAt(p.pos, "expected ';' or end of line after import block")
		}
		return nil
	}
	spec, err := p.importSpec()
	if err != nil {
		return err
	}
	prog.imports = append(prog.imports, spec)
	if !p.terminated() {
		return p.errAt(p.pos, "expected ';' or end of line after import")
	}
	return nil
}

// importSpec reads one [alias] "path" pair. The path is double-quoted
// as in Go; dot and blank imports have no meaning against a binding
// registry and are rejected.
func (p *Parser) importSpec() (importSpec, error) {
	alias := ""
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == '\'' {
		return importSpec{}, p.errAt(p.pos, "expected a double-quoted import path")
	}
	if p.pos < len(p.src) && p.src[p.pos] != '"' {
		alias = p.ident()
		if alias == "" {
			return importSpec{}, p.errAt(p.pos, "expected an import path or alias")
		}
		if alias == "_" {
			return importSpec{}, p.errAt(p.pos, "blank imports are not supported: an unused package is simply not imported")
		}
		p.skipSpace()
	}
	if p.pos >= len(p.src) || p.src[p.pos] != '"' {
		return importSpec{}, p.errAt(p.pos, "expected a double-quoted import path")
	}
	a, err := p.stringLit('"')
	if err != nil {
		return importSpec{}, err
	}
	if a.str == "" {
		return importSpec{}, p.errAt(p.pos, "empty import path")
	}
	return importSpec{alias: alias, path: a.str}, nil
}

// typeDecl is one "type Name struct{...}" or "type Name interface{...}"
// declaration.
type typeDecl struct {
	name    string
	iface   bool
	fields  []fieldDecl // struct form
	methods []methodSig // interface form
	pos     int
}

// fieldDecl is one struct field. An embedded field carries the base
// name of its type, the way Go names the implicit field.
type fieldDecl struct {
	name     string
	typ      string
	tag      string
	embedded bool
}

// param is one parameter or result: an optional name and a type in
// the registry spelling.
type param struct {
	name string
	typ  string
}

// methodSig is one method of an interface declaration.
type methodSig struct {
	name    string
	params  []param
	results []param
}

// typeDeclSniff claims "type Name struct|interface" and rewinds on
// anything else, so a hypothetical variable named type keeps parsing
// as it did.
func (p *Parser) typeDeclSniff() (typeDecl, bool, error) {
	save := p.pos
	if !p.keyword("type") {
		return typeDecl{}, false, nil
	}
	name := p.ident()
	if name != "" {
		if p.keyword("struct") {
			td, err := p.structType(name, save)
			return td, true, err
		}
		if p.keyword("interface") {
			td, err := p.ifaceType(name, save)
			return td, true, err
		}
	}
	p.pos = save
	return typeDecl{}, false, nil
}

func (p *Parser) structType(name string, pos int) (typeDecl, error) {
	td := typeDecl{name: name, pos: pos}
	if !p.consume('{') {
		return td, p.errAt(p.pos, "expected '{' after struct")
	}
	for {
		p.skipSpace()
		if p.consume('}') {
			break
		}
		if p.pos >= len(p.src) {
			return td, p.errAt(p.pos, "unterminated struct body")
		}
		fields, err := p.fieldLine()
		if err != nil {
			return td, err
		}
		td.fields = append(td.fields, fields...)
	}
	if !p.terminated() {
		return td, p.errAt(p.pos, "expected ';' or end of line after type declaration")
	}
	return td, nil
}

// fieldLine reads one line of a struct body: a name list with a type,
// or an embedded type, either with an optional tag.
func (p *Parser) fieldLine() ([]fieldDecl, error) {
	save := p.pos
	var names []string
	for {
		n := p.ident()
		if n == "" {
			names = nil
			break
		}
		names = append(names, n)
		if !p.consume(',') {
			break
		}
	}
	if names != nil && !p.atFieldEnd() && p.peek() != '.' {
		typ, err := p.typeRef()
		if err != nil {
			return nil, err
		}
		tag, err := p.fieldTag()
		if err != nil {
			return nil, err
		}
		if !p.fieldTerm() {
			return nil, p.errAt(p.pos, "expected ';' or end of line after field")
		}
		out := make([]fieldDecl, len(names))
		for i, n := range names {
			out[i] = fieldDecl{name: n, typ: typ, tag: tag}
		}
		return out, nil
	}

	// An embedded field: a bare type reference, optionally through a
	// pointer, as in Go.
	p.pos = save
	typ, err := p.typeRef()
	if err != nil {
		return nil, p.errAt(save, "expected a field or an embedded type")
	}
	base := strings.TrimPrefix(typ, "*")
	if strings.IndexAny(base, "[]<- ") >= 0 || strings.HasPrefix(typ, "**") {
		return nil, p.errAt(save, "cannot embed %s: only a type name or a pointer to one embeds", typ)
	}
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		base = base[i+1:]
	}
	tag, err := p.fieldTag()
	if err != nil {
		return nil, err
	}
	if !p.fieldTerm() {
		return nil, p.errAt(p.pos, "expected ';' or end of line after field")
	}
	return []fieldDecl{{name: base, typ: typ, tag: tag, embedded: true}}, nil
}

// atFieldEnd reports whether the next token ends a field line: a tag,
// a terminator, or the closing brace. It consumes nothing.
func (p *Parser) atFieldEnd() bool {
	save, saveNL := p.pos, p.nl
	p.skipSpace()
	end := p.pos >= len(p.src) || p.nl ||
		p.src[p.pos] == ';' || p.src[p.pos] == '}' ||
		p.src[p.pos] == '`' || p.src[p.pos] == '"'
	p.pos, p.nl = save, saveNL
	return end
}

// fieldTerm is terminated with the closing brace also ending a line,
// left for the body loop to consume.
func (p *Parser) fieldTerm() bool {
	if p.consume(';') {
		return true
	}
	p.skipSpace()
	return p.pos >= len(p.src) || p.nl || p.src[p.pos] == '}'
}

// fieldTag reads an optional struct tag on the same line: a raw
// string in the usual spelling, or a quoted string, both legal Go.
func (p *Parser) fieldTag() (string, error) {
	p.skipSpace()
	if p.nl || p.pos >= len(p.src) {
		return "", nil
	}
	var a arg
	var err error
	switch p.src[p.pos] {
	case '`':
		a, err = p.rawString()
	case '"':
		a, err = p.stringLit('"')
	default:
		return "", nil
	}
	return a.str, err
}

func (p *Parser) ifaceType(name string, pos int) (typeDecl, error) {
	td := typeDecl{name: name, iface: true, pos: pos}
	if !p.consume('{') {
		return td, p.errAt(p.pos, "expected '{' after interface")
	}
	for {
		p.skipSpace()
		if p.consume('}') {
			break
		}
		if p.pos >= len(p.src) {
			return td, p.errAt(p.pos, "unterminated interface body")
		}
		m, err := p.methodSig()
		if err != nil {
			return td, err
		}
		td.methods = append(td.methods, m)
	}
	if !p.terminated() {
		return td, p.errAt(p.pos, "expected ';' or end of line after type declaration")
	}
	return td, nil
}

func (p *Parser) methodSig() (methodSig, error) {
	name := p.ident()
	if name == "" {
		return methodSig{}, p.errAt(p.pos, "expected a method name")
	}
	if !p.consume('(') {
		return methodSig{}, p.errAt(p.pos, "expected '(' after method name; embedding an interface is not supported")
	}
	params, err := p.paramList()
	if err != nil {
		return methodSig{}, err
	}
	results, err := p.resultList()
	if err != nil {
		return methodSig{}, err
	}
	if !p.fieldTerm() {
		return methodSig{}, p.errAt(p.pos, "expected ';' or end of line after method")
	}
	return methodSig{name: name, params: params, results: results}, nil
}

// paramList reads a parameter list up to and including the closing
// paren. Go's grammar makes "a, b int64" and "int64, string" look
// alike until a group ends, so elements are collected first and bare
// idents resolve right to left, each taking the type of the next
// element that has one; a list with no named element is all types.
func (p *Parser) paramList() ([]param, error) {
	type elem struct {
		bare  string // an ident that is a name or a type, unresolved
		prm   param
		named bool
	}
	var elems []elem
	for {
		p.skipSpace()
		if p.consume(')') {
			break
		}
		if len(elems) > 0 && !p.consume(',') {
			return nil, p.errAt(p.pos, "expected ',' or ')'")
		}
		save := p.pos
		n := p.ident()
		if n != "" && !p.paramSepNext() {
			typ, err := p.typeRef()
			if err != nil {
				return nil, err
			}
			elems = append(elems, elem{prm: param{name: n, typ: typ}, named: true})
			continue
		}
		p.pos = save
		typ, err := p.typeRef()
		if err != nil {
			return nil, p.errAt(save, "expected a parameter")
		}
		if n != "" && typ == n {
			elems = append(elems, elem{bare: n})
			continue
		}
		elems = append(elems, elem{prm: param{typ: typ}})
	}

	named := false
	for _, e := range elems {
		if e.named {
			named = true
			break
		}
	}
	out := make([]param, len(elems))
	pending := ""
	for i := len(elems) - 1; i >= 0; i-- {
		e := elems[i]
		switch {
		case e.named:
			out[i] = e.prm
			pending = e.prm.typ
		case e.bare != "" && named:
			if pending == "" {
				return nil, p.errAt(p.pos, "cannot mix named and unnamed parameters")
			}
			out[i] = param{name: e.bare, typ: pending}
		case e.bare != "":
			out[i] = param{typ: e.bare}
		default:
			if named {
				return nil, p.errAt(p.pos, "cannot mix named and unnamed parameters")
			}
			out[i] = e.prm
		}
	}
	return out, nil
}

// paramSepNext reports whether the next byte separates parameters or
// qualifies the ident just read, meaning that ident was a whole
// element rather than a name with a type after it.
func (p *Parser) paramSepNext() bool {
	c := p.peek()
	return c == ',' || c == ')' || c == '.'
}

// resultList reads an optional result list on the same line: nothing,
// one bare typeref, or a parenthesised parameter list.
func (p *Parser) resultList() ([]param, error) {
	p.skipSpace()
	if p.nl || p.pos >= len(p.src) {
		return nil, nil
	}
	switch c := p.src[p.pos]; {
	case c == '(':
		p.pos++
		p.nl = false
		return p.paramList()
	case c == ';' || c == '}' || c == '{':
		return nil, nil
	}
	typ, err := p.typeRef()
	if err != nil {
		return nil, err
	}
	return []param{{typ: typ}}, nil
}
