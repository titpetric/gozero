package gozero

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"strconv"
)

// Type declarations through the standard library's front end. The
// statement parser only sniffs "type Name struct"; go/scanner decides
// where the block ends, go/parser turns the snippet into fields, and
// the struct body is whatever Go's grammar says it is: field name
// lists, comments between fields, semicolon or newline separators,
// and raw or double-quoted tags all parse because go/parser parses
// them, not because this package grew the cases. What the rung still
// rejects is semantic, named in vm_decl.go, not grammatical.

// typeDecl is one "type Name struct { ... }" declaration.
type typeDecl struct {
	name   string
	fields []fieldDecl
	// file is the wrapped snippet's AST and st the struct body inside
	// it, kept for the go/types pass declareTypes runs before building
	// the reflect types.
	file *ast.File
	st   *ast.StructType
}

// fieldDecl is one struct field: a name, the type spelling in the
// registry form, and the tag with its quotes removed, empty when the
// field has none. A field name list contributes one fieldDecl per
// name.
type fieldDecl struct {
	name string
	typ  string
	tag  string
}

// typeDecl claims "type Name struct" and rewinds on anything else, so
// a name that merely starts with those letters keeps parsing as it
// did.
func (p *Parser) typeDecl() (typeDecl, bool, error) {
	save := p.pos
	if !p.keyword("type") {
		return typeDecl{}, false, nil
	}
	start := p.pos - len("type")
	name := p.ident()
	if name == "" || !p.keyword("struct") {
		p.pos = save
		return typeDecl{}, false, nil
	}
	end, err := p.declEnd(name)
	if err != nil {
		return typeDecl{}, true, err
	}
	snippet := p.src[start:end]
	p.pos = end
	p.nl = false
	if !p.terminated() {
		return typeDecl{}, true, fmt.Errorf("parse: expected ';' or end of line at offset %d", p.pos)
	}
	td, err := p.goParse(name, snippet)
	return td, true, err
}

// declEnd scans forward from the sniffed "struct" keyword with
// go/scanner and returns the offset one past the brace that closes the
// declaration. The scanner, not a hand loop, decides what a brace
// inside a string, a raw string or a comment means, which is the whole
// reason to use it.
func (p *Parser) declEnd(name string) (int, error) {
	if p.fset == nil {
		p.fset = token.NewFileSet()
	}
	base := p.pos
	rest := p.src[base:]
	file := p.fset.AddFile("scan", -1, len(rest))
	var scanErr error
	var s scanner.Scanner
	s.Init(file, []byte(rest), func(pos token.Position, msg string) {
		if scanErr == nil {
			scanErr = fmt.Errorf("parse: type %s: %s at offset %d", name, msg, base+pos.Offset)
		}
	}, 0)
	depth := 0
	for {
		pos, tok, _ := s.Scan()
		if scanErr != nil {
			return 0, scanErr
		}
		if depth == 0 && tok != token.LBRACE {
			return 0, fmt.Errorf("parse: type %s: expected '{' after struct at offset %d", name, base+file.Offset(pos))
		}
		switch tok {
		case token.LBRACE:
			depth++
		case token.RBRACE:
			depth--
			if depth == 0 {
				return base + file.Offset(pos) + 1, nil
			}
		case token.EOF:
			return 0, fmt.Errorf("parse: type %s: unterminated struct body at offset %d", name, len(p.src))
		}
	}
}

// goParse hands the captured snippet to go/parser wrapped as a file
// and reads the fields off the AST. The declaration is Go source
// verbatim, so the wrap is the package clause and nothing else.
func (p *Parser) goParse(name, snippet string) (typeDecl, error) {
	file, err := parser.ParseFile(p.fset, "typedecl", "package p\n\n"+snippet+"\n", parser.SkipObjectResolution)
	if err != nil {
		return typeDecl{}, fmt.Errorf("parse: type %s: %s", name, goErrMsg(err))
	}
	td := typeDecl{name: name, file: file}
	st, ok := declStruct(file)
	if !ok {
		return typeDecl{}, fmt.Errorf("parse: type %s: not a struct declaration", name)
	}
	td.st = st
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 {
			return typeDecl{}, fmt.Errorf("parse: type %s: an embedded field is not supported, name the field", name)
		}
		if _, ok := f.Type.(*ast.StructType); ok {
			return typeDecl{}, fmt.Errorf("parse: type %s: an inline struct field is not supported, declare it as its own type", name)
		}
		tag := ""
		if f.Tag != nil {
			tag, err = strconv.Unquote(f.Tag.Value)
			if err != nil {
				return typeDecl{}, fmt.Errorf("parse: type %s: bad struct tag %s", name, f.Tag.Value)
			}
		}
		// types.ExprString prints the field type the way
		// reflect.Type.String does, which is the registry's key form.
		typ := types.ExprString(f.Type)
		for _, n := range f.Names {
			td.fields = append(td.fields, fieldDecl{name: n.Name, typ: typ, tag: tag})
		}
	}
	return td, nil
}

// declStruct digs the struct body out of the wrapped file. The sniff
// and the brace scan guarantee the shape; this only refuses to build
// a typeDecl from anything else.
func declStruct(file *ast.File) (*ast.StructType, bool) {
	if len(file.Decls) != 1 {
		return nil, false
	}
	gd, ok := file.Decls[0].(*ast.GenDecl)
	if !ok || len(gd.Specs) != 1 {
		return nil, false
	}
	ts, ok := gd.Specs[0].(*ast.TypeSpec)
	if !ok {
		return nil, false
	}
	st, ok := ts.Type.(*ast.StructType)
	return st, ok
}

// goErrMsg keeps the first message of a go/parser error list: the
// position points into a wrapped snippet, so only Go's words carry
// information.
func goErrMsg(err error) string {
	var list scanner.ErrorList
	if errors.As(err, &list) && len(list) > 0 {
		return list[0].Msg
	}
	return err.Error()
}
