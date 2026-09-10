package gozero

import (
	"fmt"
	"reflect"
	"strings"
)

// cscope is the per-compilation view threaded through the compile
// functions: name-to-slot and name-to-type maps, and the types the
// program itself declares. It is per-Compile state, never stored on
// the Compiler, which concurrent compilations share.
type cscope struct {
	slots  map[string]int
	env    map[string]reflect.Type
	script *scriptTypes
	// parent chains block scopes: name resolution walks outward, a
	// declaration lands in the innermost scope, and := may shadow an
	// outer name with a fresh slot, as in Go.
	parent *cscope
	// bindings is what dotted paths resolve against: the Compiler's
	// flat map for a snippet, a map assembled from the import block
	// for a file.
	bindings map[string]binding
	// pkgs maps the local name of each import to its package; non-nil
	// exactly when the program has a file header, which is also what
	// scopes type resolution to the imports.
	pkgs map[string]*boundPackage
}

// scriptTypes holds the types a program declares in its own source:
// struct types built once per compilation with reflect.StructOf, and
// interface declarations kept as method-set tables, because reflect
// cannot mint an interface type at run time. reflect.StructOf
// canonicalizes, so recompiling the same source costs nothing beyond
// the calls; the names map is the reverse index diagnostics use to
// print the declared name instead of the anonymous struct spelling.
type scriptTypes struct {
	structs map[string]reflect.Type
	names   map[reflect.Type]string
	ifaces  map[string]*scriptIface
}

// scriptIface is one declared interface: a name and the method set a
// value must cover. Values of the type travel as any; satisfaction is
// checked structurally at compile time.
type scriptIface struct {
	name    string
	methods []scriptMethod
}

// scriptMethod is one method of a declared interface, its parameter
// and result types resolved.
type scriptMethod struct {
	name    string
	params  []reflect.Type
	results []reflect.Type
}

// child opens a block scope: fresh name maps, everything else shared
// by reference.
func (sc *cscope) child() *cscope {
	return &cscope{
		slots:    map[string]int{},
		env:      map[string]reflect.Type{},
		script:   sc.script,
		parent:   sc,
		bindings: sc.bindings,
		pkgs:     sc.pkgs,
	}
}

// slot resolves a name to its slot through the scope chain.
func (sc *cscope) slot(name string) (int, bool) {
	for s := sc; s != nil; s = s.parent {
		if slot, ok := s.slots[name]; ok {
			return slot, true
		}
	}
	return 0, false
}

// typeOf is the static type of a name through the scope chain, nil
// when the name is not bound by the program.
func (sc *cscope) typeOf(name string) reflect.Type {
	for s := sc; s != nil; s = s.parent {
		if t, ok := s.env[name]; ok {
			return t
		}
	}
	return nil
}

// typeName is the spelling diagnostics use for t: the declared name
// when the program declared it, so an error says Point rather than
// the anonymous struct spelling, and reflect's own otherwise.
func (sc *cscope) typeName(t reflect.Type) string {
	if sc != nil && sc.script != nil {
		if name, ok := sc.script.names[t]; ok {
			return name
		}
	}
	return t.String()
}

// predeclaredTypes is the immutable seed set, nameable in any mode.
// The registry each Runtime mutates is its own copy.
var predeclaredTypes = predeclared()

// resolveType resolves a type name: compound spellings construct
// their type structurally, base names resolve against the program's
// own declarations, then, for a file, its imports and the predeclared
// names only; a snippet falls through to the shared registry.
func (c *Compiler) resolveType(sc *cscope, name string) (reflect.Type, bool) {
	if t, ok := c.resolveTypeExpr(sc, name); ok {
		return t, true
	}
	if sc != nil && sc.script != nil {
		if t, ok := sc.script.structs[name]; ok {
			return t, true
		}
		if _, ok := sc.script.ifaces[name]; ok {
			// A declared interface has no reflect type; values of it
			// travel as any and are checked structurally.
			return anyType, true
		}
	}
	if sc != nil && sc.pkgs != nil {
		if i := strings.IndexByte(name, '.'); i > 0 {
			if pkg := sc.pkgs[name[:i]]; pkg != nil {
				// The table is keyed by reflect spellings, which carry
				// the real base name whatever the import's alias.
				if t, ok := pkg.types[pkg.base+name[i:]]; ok {
					return t, true
				}
			}
			return nil, false
		}
		t, ok := predeclaredTypes[name]
		return t, ok
	}
	return c.lookupType(name)
}

// resolveTypeExpr builds a compound type from its spelling when the
// registry has no exact entry: "*X", "[]X", the channel forms and
// "map[K]V", recursing on the element. The spelling is the parser's,
// which matches reflect.Type.String.
func (c *Compiler) resolveTypeExpr(sc *cscope, spec string) (reflect.Type, bool) {
	switch {
	case strings.HasPrefix(spec, "*"):
		if t, ok := c.resolveType(sc, spec[1:]); ok {
			return reflect.PointerTo(t), true
		}
	case strings.HasPrefix(spec, "[]"):
		if t, ok := c.resolveType(sc, spec[2:]); ok {
			return reflect.SliceOf(t), true
		}
	case strings.HasPrefix(spec, "<-chan "):
		if t, ok := c.resolveType(sc, spec[7:]); ok {
			return reflect.ChanOf(reflect.RecvDir, t), true
		}
	case strings.HasPrefix(spec, "chan<- "):
		if t, ok := c.resolveType(sc, spec[7:]); ok {
			return reflect.ChanOf(reflect.SendDir, t), true
		}
	case strings.HasPrefix(spec, "chan "):
		if t, ok := c.resolveType(sc, spec[5:]); ok {
			return reflect.ChanOf(reflect.BothDir, t), true
		}
	case strings.HasPrefix(spec, "map["):
		key, elem, ok := splitMapSpec(spec)
		if !ok {
			return nil, false
		}
		kt, ok := c.resolveType(sc, key)
		if !ok || !kt.Comparable() {
			return nil, false
		}
		et, ok := c.resolveType(sc, elem)
		if !ok {
			return nil, false
		}
		return reflect.MapOf(kt, et), true
	}
	return nil, false
}

// splitMapSpec splits "map[K]V" into K and V at the bracket matching
// the opening one, so a map key that is itself a map still splits at
// the right place.
func splitMapSpec(spec string) (string, string, bool) {
	depth := 0
	for i := 4; i < len(spec); i++ {
		switch spec[i] {
		case '[':
			depth++
		case ']':
			if depth == 0 {
				return spec[4:i], spec[i+1:], true
			}
			depth--
		}
	}
	return "", "", false
}

// buildScriptTypes builds the program's declared types before any
// statement compiles. Structs resolve in dependency order over as
// many passes as it takes, so a declaration can reference one written
// after it; what never resolves is undefined or recursive, which
// reflect.StructOf cannot express even through a pointer.
func (c *Compiler) buildScriptTypes(sc *cscope, prog *program) error {
	if len(prog.types) == 0 {
		return nil
	}
	st := &scriptTypes{
		structs: map[string]reflect.Type{},
		names:   map[reflect.Type]string{},
		ifaces:  map[string]*scriptIface{},
	}
	sc.script = st
	seen := map[string]bool{}
	for _, td := range prog.types {
		if seen[td.name] {
			return fmt.Errorf("compile: type %s redeclared", td.name)
		}
		seen[td.name] = true
		if _, ok := c.resolveType(sc, td.name); ok {
			return fmt.Errorf("compile: type %s shadows a registered type", td.name)
		}
		if td.iface {
			st.ifaces[td.name] = &scriptIface{name: td.name}
		}
	}

	pending := make([]*typeDecl, 0, len(prog.types))
	for i := range prog.types {
		if !prog.types[i].iface {
			pending = append(pending, &prog.types[i])
		}
	}
	for len(pending) > 0 {
		progress := false
		rest := pending[:0]
		for _, td := range pending {
			t, ok, err := c.buildStruct(sc, td)
			if err != nil {
				return err
			}
			if !ok {
				rest = append(rest, td)
				continue
			}
			st.structs[td.name] = t
			st.names[t] = td.name
			progress = true
		}
		pending = rest
		if !progress {
			td := pending[0]
			for _, fd := range td.fields {
				if _, ok := c.resolveType(sc, fd.typ); !ok {
					return fmt.Errorf("compile: type %s: field type %q is undefined or recursive; a script struct cannot contain itself, even through a pointer", td.name, fd.typ)
				}
			}
			return fmt.Errorf("compile: type %s cannot be built", td.name)
		}
	}

	// Interface method signatures resolve after every struct exists,
	// so a method can mention a declared type.
	for _, td := range prog.types {
		if !td.iface {
			continue
		}
		iface := st.ifaces[td.name]
		for _, m := range td.methods {
			sm := scriptMethod{name: m.name}
			for _, prm := range m.params {
				t, ok := c.resolveType(sc, prm.typ)
				if !ok {
					return fmt.Errorf("compile: interface %s: method %s: unknown type %q", td.name, m.name, prm.typ)
				}
				sm.params = append(sm.params, t)
			}
			for _, r := range m.results {
				t, ok := c.resolveType(sc, r.typ)
				if !ok {
					return fmt.Errorf("compile: interface %s: method %s: unknown type %q", td.name, m.name, r.typ)
				}
				sm.results = append(sm.results, t)
			}
			iface.methods = append(iface.methods, sm)
		}
	}
	return nil
}

// buildStruct builds one declared struct if every field type already
// resolves, reporting ok=false to try again after other declarations
// land. Errors are for what another pass cannot fix.
func (c *Compiler) buildStruct(sc *cscope, td *typeDecl) (reflect.Type, bool, error) {
	fields := make([]reflect.StructField, 0, len(td.fields))
	seen := map[string]bool{}
	for _, fd := range td.fields {
		if seen[fd.name] {
			return nil, false, fmt.Errorf("compile: type %s: duplicate field %s", td.name, fd.name)
		}
		seen[fd.name] = true
		t, ok := c.resolveType(sc, fd.typ)
		if !ok {
			return nil, false, nil
		}
		if fd.embedded {
			// reflect.StructOf panics on an embedded field whose type
			// has methods, because it cannot generate the promotion
			// wrappers; the check keeps the panic out of Compile.
			mt := t
			if mt.Kind() == reflect.Pointer {
				mt = mt.Elem()
			}
			if t.NumMethod() > 0 || (mt.Kind() != reflect.Interface && reflect.PointerTo(mt).NumMethod() > 0) {
				return nil, false, fmt.Errorf("compile: type %s: cannot embed %s: reflect.StructOf cannot promote its methods", td.name, fd.typ)
			}
		}
		if fd.name == "" || fd.name[0] >= 'a' && fd.name[0] <= 'z' || fd.name[0] == '_' {
			return nil, false, fmt.Errorf("compile: type %s: field %s must be exported; reflect.StructOf cannot build unexported fields", td.name, fd.name)
		}
		fields = append(fields, reflect.StructField{
			Name:      fd.name,
			Type:      t,
			Tag:       reflect.StructTag(fd.tag),
			Anonymous: fd.embedded,
		})
	}
	return reflect.StructOf(fields), true, nil
}
