package gozero

import (
	"fmt"
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

// declareTypes builds prog's declared types into c.declared.
// Declarations resolve in dependency order over as many passes as it
// takes, so a field can name a type declared after it; what never
// resolves is undefined, recursive, or a spelling this grammar does
// not construct.
func (c *Compiler) declareTypes(prog *program) error {
	c.declared = make(map[string]reflect.Type, len(prog.types))
	declared := make(map[string]bool, len(prog.types))
	bound := map[string]bool{}
	for name := range c.bindings {
		if i := strings.IndexByte(name, '.'); i > 0 {
			bound[name[:i]] = true
		} else {
			bound[name] = true
		}
	}
	for i := range prog.types {
		td := &prog.types[i]
		if declared[td.name] {
			return fmt.Errorf("compile: type %s redeclared", td.name)
		}
		declared[td.name] = true
		if _, ok := c.types[td.name]; ok {
			return fmt.Errorf("compile: type %s shadows a registered type", td.name)
		}
		if bound[td.name] {
			return fmt.Errorf("compile: type %s shadows a binding", td.name)
		}
	}

	pending := make([]*typeDecl, 0, len(prog.types))
	for i := range prog.types {
		pending = append(pending, &prog.types[i])
	}
	for len(pending) > 0 {
		progress := false
		rest := pending[:0]
		for _, td := range pending {
			t, ok, err := c.buildStructType(td)
			if err != nil {
				return err
			}
			if !ok {
				rest = append(rest, td)
				continue
			}
			c.declared[td.name] = t
			progress = true
		}
		pending = rest
		if !progress {
			return c.unresolvedTypes(pending, declared)
		}
	}
	return nil
}

// buildStructType builds one declared struct when every field type
// already resolves, reporting ok=false to retry after other
// declarations land. Errors are for what another pass cannot fix:
// each names the reflect.StructOf ceiling it keeps out of Compile.
func (c *Compiler) buildStructType(td *typeDecl) (reflect.Type, bool, error) {
	fields := make([]reflect.StructField, 0, len(td.fields))
	seen := map[string]bool{}
	for _, fd := range td.fields {
		if seen[fd.name] {
			return nil, false, fmt.Errorf("compile: type %s: duplicate field %s", td.name, fd.name)
		}
		seen[fd.name] = true
		// The lexer's identifiers are ASCII, so one byte decides.
		if fd.name[0] < 'A' || fd.name[0] > 'Z' {
			if fd.embedded {
				return nil, false, fmt.Errorf("compile: type %s: embedding %s makes an unexported field name; reflect.StructOf cannot build unexported fields", td.name, fd.typ)
			}
			return nil, false, fmt.Errorf("compile: type %s: field %s must be exported; reflect.StructOf cannot build unexported fields", td.name, fd.name)
		}
		t, ok := c.lookupType(fd.typ)
		if !ok {
			return nil, false, nil
		}
		if fd.embedded {
			// Records only. A method-carrying embed cannot keep Go's
			// promotion promise: reflect.StructOf drops pointer methods
			// silently instead of promoting them, so the whole method
			// set is refused rather than half-kept.
			if t.Kind() != reflect.Struct {
				return nil, false, fmt.Errorf("compile: type %s: embedded field %s is not a struct; a declared struct embeds records only", td.name, fd.typ)
			}
			if reflect.PointerTo(t).NumMethod() > 0 {
				return nil, false, fmt.Errorf("compile: type %s: embedded type %s carries methods, and a declared struct is a record; reflect.StructOf drops pointer methods instead of promoting them", td.name, fd.typ)
			}
		}
		// The tag passes through as written; it is part of the type's
		// identity, so two declarations differing only in tags mint two
		// runtime types. So is the embedded flag: the same fields named
		// and embedded are two distinct types.
		fields = append(fields, reflect.StructField{Name: fd.name, Type: t, Tag: reflect.StructTag(fd.tag), Anonymous: fd.embedded})
	}
	return reflect.StructOf(fields), true, nil
}

// unresolvedTypes turns a resolution pass that made no progress into
// the error naming why. An unknown name is the root cause when there
// is one: a recursion report for a struct whose dependency merely
// failed would point at the wrong declaration.
func (c *Compiler) unresolvedTypes(pending []*typeDecl, declared map[string]bool) error {
	for _, td := range pending {
		for _, fd := range td.fields {
			if _, ok := c.lookupType(fd.typ); ok {
				continue
			}
			if !declared[declBase(fd.typ)] {
				return fmt.Errorf("compile: type %s: unknown field type %q, register it with BindType", td.name, fd.typ)
			}
		}
	}
	for _, td := range pending {
		for _, fd := range td.fields {
			if _, ok := c.lookupType(fd.typ); ok {
				continue
			}
			if declBase(fd.typ) != fd.typ {
				return fmt.Errorf("compile: type %s: field type %s does not resolve; a field holds a declared type by value only", td.name, fd.typ)
			}
		}
	}
	return fmt.Errorf("compile: type %s is recursive; reflect.StructOf cannot build a struct that contains itself, even through a pointer", pending[0].name)
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
