package gozero

import (
	"fmt"
	"reflect"
	"strings"
)

// A registered package is a set of bindings under a Go import path,
// with its own type table filled by walking the signatures. A program
// with a package header reaches a registered package only through its
// import block; Import activates one into the flat namespace for
// headerless snippets.

// boundPackage is one registered package.
type boundPackage struct {
	path  string
	base  string // last path element: "net/http" -> "http"
	fns   map[string]binding
	types map[string]reflect.Type
}

// pkgBase is the name a package binds under when an import does not
// alias it: the last element of the import path.
func pkgBase(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// buildNamespace fills the scope's binding view. A file resolves
// names only through its import block, and an import of an
// unregistered path is the load-time error the registration contract
// promises; a snippet keeps the Compiler's flat map.
func (c *Compiler) buildNamespace(sc *cscope, prog *program) error {
	if prog.pkg == "" {
		sc.bindings = c.bindings
		return nil
	}
	sc.bindings = map[string]binding{}
	sc.pkgs = map[string]*boundPackage{}
	for _, imp := range prog.imports {
		pkg, ok := c.packages[imp.path]
		if !ok {
			return fmt.Errorf("compile: import %q: package is not registered, call BindPackage", imp.path)
		}
		local := imp.alias
		if local == "" {
			local = pkg.base
		}
		if sc.pkgs[local] != nil {
			return fmt.Errorf("compile: %s redeclared as imported package name", local)
		}
		sc.pkgs[local] = pkg
		for fname, b := range pkg.fns {
			sc.bindings[local+"."+fname] = b
		}
	}
	return nil
}
