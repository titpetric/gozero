package gozero

import (
	"fmt"
)

// Type declarations: the grammar's first block form. A declaration is
// braces around field lines, one field per line or per semicolon, no
// nesting beyond the braces:
//
//	typedecl := "type" name "struct" "{" { field } "}" term
//	field    := name typeref fterm
//	fterm    := ";" | EOL | "}"
//
// The parser records name and field spellings; the compiler builds the
// reflect types and owns every semantic check. What Go's struct
// grammar has beyond this is named when it is seen - a field name
// list, an embedded type, a tag - rather than misparsed.

// typeDecl is one "type Name struct { ... }" declaration.
type typeDecl struct {
	name   string
	fields []fieldDecl
}

// fieldDecl is one struct field: a name and a type in the registry
// spelling.
type fieldDecl struct {
	name string
	typ  string
}

// typeDecl claims "type Name struct" and rewinds on anything else, so
// a name that merely starts with those letters keeps parsing as it
// did.
func (p *Parser) typeDecl() (typeDecl, bool, error) {
	save := p.pos
	if !p.keyword("type") {
		return typeDecl{}, false, nil
	}
	name := p.ident()
	if name == "" || !p.keyword("struct") {
		p.pos = save
		return typeDecl{}, false, nil
	}
	td, err := p.structType(name)
	return td, true, err
}

func (p *Parser) structType(name string) (typeDecl, error) {
	td := typeDecl{name: name}
	if !p.consume('{') {
		return td, fmt.Errorf("parse: expected '{' after struct at offset %d", p.pos)
	}
	for {
		p.skipSpace()
		if p.consume('}') {
			break
		}
		if p.pos >= len(p.src) {
			return td, fmt.Errorf("parse: type %s: unterminated struct body at offset %d", name, p.pos)
		}
		fd, err := p.fieldDecl(name)
		if err != nil {
			return td, err
		}
		td.fields = append(td.fields, fd)
	}
	if !p.terminated() {
		return td, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
	}
	return td, nil
}

// fieldDecl reads one field line. The forms Go allows here that this
// grammar does not are each rejected by name.
func (p *Parser) fieldDecl(typeName string) (fieldDecl, error) {
	name := p.ident()
	if name == "" {
		return fieldDecl{}, fmt.Errorf("parse: type %s: expected a field name at offset %d", typeName, p.pos)
	}
	if p.peek() == ',' {
		return fieldDecl{}, fmt.Errorf("parse: type %s: a field name list is not supported, declare one field per line", typeName)
	}
	if p.peek() == '.' || p.atFieldEnd() {
		return fieldDecl{}, fmt.Errorf("parse: type %s: an embedded field is not supported, name the field", typeName)
	}
	typ, err := p.typeRef()
	if err != nil {
		return fieldDecl{}, err
	}
	// A tag would silently vanish if the line simply had to end here;
	// the raw-string token does not exist in the lexer yet, so a tag
	// is named instead.
	save, saveNL := p.pos, p.nl
	p.skipSpace()
	if !p.nl && p.pos < len(p.src) && (p.src[p.pos] == '`' || p.src[p.pos] == '"') {
		return fieldDecl{}, fmt.Errorf("parse: type %s: field tags are not supported", typeName)
	}
	p.pos, p.nl = save, saveNL
	if !p.fieldTerm() {
		return fieldDecl{}, fmt.Errorf("parse: type %s: expected ';' or end of line after field at offset %d", typeName, p.pos)
	}
	return fieldDecl{name: name, typ: typ}, nil
}

// atFieldEnd reports whether the next token ends a field line,
// consuming nothing: a lone identifier on a line is an embedded type,
// not a field.
func (p *Parser) atFieldEnd() bool {
	save, saveNL := p.pos, p.nl
	p.skipSpace()
	end := p.pos >= len(p.src) || p.nl || p.src[p.pos] == ';' || p.src[p.pos] == '}'
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
