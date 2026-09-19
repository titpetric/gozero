package gozero

import (
	"fmt"
)

// Type declarations: the grammar's first block form. A declaration is
// braces around field lines, one field per line or per semicolon, no
// nesting beyond the braces:
//
//	typedecl := "type" name "struct" "{" { field } "}" term
//	field    := name typeref [ tag ] fterm
//	         | path [ tag ] fterm
//	tag      := rawstring | string
//	fterm    := ";" | EOL | "}"
//
// The parser records name, field and tag spellings; the compiler
// builds the reflect types and owns every semantic check. What Go's
// struct grammar has beyond this is named when it is seen - a field
// name list, an embedded pointer - rather than misparsed.

// typeDecl is one "type Name struct { ... }" declaration.
type typeDecl struct {
	name   string
	fields []fieldDecl
}

// fieldDecl is one struct field: a name, a type in the registry
// spelling, and the tag as written, empty when the field has none.
// An embedded field is a type standing alone on its line; its name is
// the type's base segment, as in Go, and typ is the full spelling.
type fieldDecl struct {
	name     string
	typ      string
	tag      string
	embedded bool
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

// fieldDecl reads one field line: a named field, or a type standing
// alone, which is an embedded field. The forms Go allows here that
// this grammar does not are each rejected by name.
func (p *Parser) fieldDecl(typeName string) (fieldDecl, error) {
	if p.peek() == '*' {
		return fieldDecl{}, fmt.Errorf("parse: type %s: an embedded pointer field is not supported, embed the struct by value", typeName)
	}
	name := p.ident()
	if name == "" {
		return fieldDecl{}, fmt.Errorf("parse: type %s: expected a field name at offset %d", typeName, p.pos)
	}
	if p.peek() == ',' {
		return fieldDecl{}, fmt.Errorf("parse: type %s: a field name list is not supported, declare one field per line", typeName)
	}
	if p.peek() == '.' || p.atFieldEnd() {
		return p.embeddedField(typeName, name)
	}
	typ, err := p.typeRef()
	if err != nil {
		return fieldDecl{}, err
	}
	tag, err := p.fieldTag(typeName)
	if err != nil {
		return fieldDecl{}, err
	}
	if !p.fieldTerm() {
		return fieldDecl{}, fmt.Errorf("parse: type %s: expected ';' or end of line after field at offset %d", typeName, p.pos)
	}
	return fieldDecl{name: name, typ: typ, tag: tag}, nil
}

// embeddedField reads the rest of an embedded field line: the dotted
// remainder of the type path and an optional tag, both legal Go. The
// field's name is the type's base segment, which is Go's rule too.
func (p *Parser) embeddedField(typeName, first string) (fieldDecl, error) {
	typ := first
	name := first
	for p.peek() == '.' {
		p.consume('.')
		seg := p.ident()
		if seg == "" {
			return fieldDecl{}, fmt.Errorf("parse: type %s: expected a name after '.' at offset %d", typeName, p.pos)
		}
		typ += "." + seg
		name = seg
	}
	tag, err := p.fieldTag(typeName)
	if err != nil {
		return fieldDecl{}, err
	}
	if !p.fieldTerm() {
		return fieldDecl{}, fmt.Errorf("parse: type %s: expected ';' or end of line after field at offset %d", typeName, p.pos)
	}
	return fieldDecl{name: name, typ: typ, tag: tag, embedded: true}, nil
}

// fieldTag reads an optional tag on the field's own line: a raw
// string in the usual spelling or a double-quoted string, both legal
// Go. The value reaches reflect.StructField.Tag exactly as written.
func (p *Parser) fieldTag(typeName string) (string, error) {
	save, saveNL := p.pos, p.nl
	p.skipSpace()
	if p.nl || p.pos >= len(p.src) {
		p.pos, p.nl = save, saveNL
		return "", nil
	}
	var a arg
	var err error
	switch p.src[p.pos] {
	case '`':
		a, err = p.rawString()
	case '"':
		a, err = p.stringLit('"')
	case '\'':
		// A single-quoted string is a literal elsewhere in the grammar,
		// but Go spells a tag raw or double-quoted; naming that beats
		// the terminator error the line would otherwise get.
		return "", fmt.Errorf("parse: type %s: a struct tag is a raw or double-quoted string, not single-quoted", typeName)
	default:
		p.pos, p.nl = save, saveNL
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return a.str, nil
}

// atFieldEnd reports whether the next token ends a field line,
// consuming nothing: a lone identifier on a line is an embedded type,
// not a field. A tag ends the line too, so an embedded type carrying
// one parses as embedded rather than misread as a typeref.
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
