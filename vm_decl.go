package gozero

import (
	"errors"
	"fmt"
	"go/ast"
	"go/types"
	"reflect"
	"strings"
)

// Program-declared struct types. declareTypes runs before any
// statement compiles, on a per-compilation copy of the Compiler, so
// the shared instance concurrent compilations read never sees the
// program-local names. reflect.StructOf canonicalizes: structurally
// identical declarations return the identical runtime type, so
// recompiling one source mints nothing new, but every distinct shape
// stays in the process for its lifetime. The name itself never
// reaches reflect: a declared type prints as the unnamed struct
// spelling in %T output and in error text.
//
// The structural judgments belong to go/types here: the declarations
// check as one synthetic package, so a duplicate field, a redeclared
// name and value recursion are Go's own errors, and the reflect build
// below is plain recursion because the checker has already ruled a
// cycle out. What go/types cannot see is the registry, so field types
// it would call undefined are swapped for a placeholder before the
// check and resolved by lookupType after it.

// declareTypes builds prog's declared types into c.declared.
func (c *Compiler) declareTypes(prog *program) error {
	// The registry-side checks stay by hand: go/types knows nothing of
	// the bindings or the shared type registry a declaration could
	// shadow.
	bound := map[string]bool{}
	for name := range c.bindings {
		if i := strings.IndexByte(name, '.'); i > 0 {
			bound[name[:i]] = true
		} else {
			bound[name] = true
		}
	}
	byName := make(map[string]*typeDecl, len(prog.types))
	for i := range prog.types {
		td := &prog.types[i]
		if _, ok := c.types[td.name]; ok {
			return fmt.Errorf("compile: type %s shadows a registered type", td.name)
		}
		if bound[td.name] {
			return fmt.Errorf("compile: type %s shadows a binding", td.name)
		}
		if byName[td.name] == nil {
			byName[td.name] = td
		}
	}

	files := make([]*ast.File, 0, len(prog.types))
	for i := range prog.types {
		td := &prog.types[i]
		substFields(td.st, byName)
		files = append(files, td.file)
	}
	conf := types.Config{}
	if _, err := conf.Check("p", prog.fset, files, nil); err != nil {
		return fmt.Errorf("compile: type declarations: %s", typesErrMsg(err))
	}

	c.declared = make(map[string]reflect.Type, len(prog.types))
	for i := range prog.types {
		if _, err := c.buildDeclared(byName, &prog.types[i]); err != nil {
			return err
		}
	}
	return nil
}

// substFields swaps every field type only the registry can resolve
// for a placeholder the universe has, leaving the checker exactly the
// references between declared types. The original spelling is already
// recorded in the fieldDecl, and after this walk the AST's only
// reader is go/types.
func substFields(st *ast.StructType, declared map[string]*typeDecl) {
	for _, f := range st.Fields.List {
		if id, ok := f.Type.(*ast.Ident); ok && declared[id.Name] != nil {
			continue
		}
		f.Type = ast.NewIdent("bool")
	}
}

// buildDeclared builds one declared struct, building the declared
// types its fields hold first. go/types has already ruled out a value
// cycle, so the recursion terminates, and it owns duplicate fields,
// which is why no seen map guards the loop. The errors left are the
// reflect.StructOf ceilings and the registry misses go/types was
// blinded to.
func (c *Compiler) buildDeclared(byName map[string]*typeDecl, td *typeDecl) (reflect.Type, error) {
	if t, ok := c.declared[td.name]; ok {
		return t, nil
	}
	fields := make([]reflect.StructField, 0, len(td.fields))
	for _, fd := range td.fields {
		// go/parser's identifiers can open with any letter; one byte
		// decides for the ASCII names and unicode.IsUpper would for the
		// rest, but reflect.StructOf wants an exported name either way.
		if fd.name[0] < 'A' || fd.name[0] > 'Z' {
			return nil, fmt.Errorf("compile: type %s: field %s must be exported; reflect.StructOf cannot build unexported fields", td.name, fd.name)
		}
		var ft reflect.Type
		switch dep := byName[fd.typ]; {
		case dep != nil:
			t, err := c.buildDeclared(byName, dep)
			if err != nil {
				return nil, err
			}
			ft = t
		default:
			t, ok := c.lookupType(fd.typ)
			if !ok {
				if base := declBase(fd.typ); base != fd.typ && byName[base] != nil {
					return nil, fmt.Errorf("compile: type %s: field type %s does not resolve; a field holds a declared type by value only", td.name, fd.typ)
				}
				return nil, fmt.Errorf("compile: type %s: unknown field type %q, register it with BindType", td.name, fd.typ)
			}
			ft = t
		}
		// The tag passes through as written; it is part of the type's
		// identity, so two declarations differing only in tags mint two
		// runtime types.
		fields = append(fields, reflect.StructField{Name: fd.name, Type: ft, Tag: reflect.StructTag(fd.tag)})
	}
	t := reflect.StructOf(fields)
	c.declared[td.name] = t
	return t, nil
}

// declBase strips the typeref prefixes off a field spelling, so the
// diagnosis can tell a declared type behind a pointer from a name
// nothing declares.
func declBase(spec string) string {
	for {
		next := spec
		next = strings.TrimPrefix(next, "*")
		next = strings.TrimPrefix(next, "[]")
		next = strings.TrimPrefix(next, "chan<- ")
		next = strings.TrimPrefix(next, "<-chan ")
		next = strings.TrimPrefix(next, "chan ")
		if next == spec {
			return spec
		}
		spec = next
	}
}

// typesErrMsg keeps go/types' own words: the position points into a
// wrapped snippet, so it is dropped, and a cycle report's
// continuation lines go with it.
func typesErrMsg(err error) string {
	msg := err.Error()
	var te types.Error
	if errors.As(err, &te) {
		msg = te.Msg
	}
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return msg
}
