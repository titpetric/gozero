package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/titpetric/gozero"
)

// Loader compiles plugin sources against one Runtime. The Runtime's
// BindPackage registrations are what an opened file's import block
// resolves against.
type Loader struct {
	Runtime *gozero.Runtime

	mu    sync.Mutex
	files map[string]*file
}

// NewLoader returns a Loader over rt.
func NewLoader(rt *gozero.Runtime) *Loader {
	return &Loader{Runtime: rt, files: map[string]*file{}}
}

// Default is the loader the package-level Open uses. A host
// registers packages on Default.Runtime before opening plugins.
var Default = NewLoader(gozero.NewRuntime())

// Open loads path, a .go file or a folder holding one package. The
// path canonicalizes, and a second Open of the same path returns the
// same handle without rereading, as the standard library's Open
// does; freshness is explicit through Reload.
func (l *Loader) Open(path string) (Plugin, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("plugin: open %s: %w", path, err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if f := l.files[abs]; f != nil {
		return f, nil
	}
	f := &file{loader: l, path: abs, symbols: map[string]Symbol{}}
	v, err := f.load()
	if err != nil {
		return nil, err
	}
	f.current.Store(v)
	l.files[abs] = f
	return f, nil
}

// file is the concrete handle: the live version swaps atomically
// under the symbols already handed out.
type file struct {
	loader  *Loader
	path    string
	mu      sync.Mutex
	current atomic.Pointer[version]
	symbols map[string]Symbol
}

// version is one compiled state of the source: the program and the
// materialized func per symbol handed out so far.
type version struct {
	prog *gozero.Program
	fns  map[string]reflect.Value
}

// load compiles the path as it is on disk right now.
func (f *file) load() (*version, error) {
	info, err := os.Stat(f.path)
	if err != nil {
		return nil, fmt.Errorf("plugin: open %s: %w", f.path, err)
	}
	var prog *gozero.Program
	if info.IsDir() {
		prog, err = f.loader.Runtime.LoadDir(f.path)
	} else {
		prog, err = f.loader.Runtime.LoadFile(f.path)
	}
	if err != nil {
		return nil, fmt.Errorf("plugin: open %s: %w", f.path, err)
	}
	if prog.Package() == "" {
		return nil, fmt.Errorf("plugin: open %s: a plugin needs a package clause", f.path)
	}
	return &version{prog: prog, fns: map[string]reflect.Value{}}, nil
}

// Lookup resolves an exported function. The returned Symbol is a
// func value of the declared signature whose body reads the live
// version at call entry, so it survives and follows reloads.
func (f *file) Lookup(symName string) (Symbol, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sym, ok := f.symbols[symName]; ok {
		return sym, nil
	}
	r, _ := utf8.DecodeRuneInString(symName)
	if !unicode.IsUpper(r) {
		return nil, fmt.Errorf("plugin: symbol %s is not exported", symName)
	}
	cur := f.current.Load()
	sig, ok := cur.prog.Signature(symName)
	if !ok {
		return nil, fmt.Errorf("plugin: symbol %s not found in plugin %s", symName, f.path)
	}
	if err := f.materialize(cur, symName); err != nil {
		return nil, err
	}
	sym := reflect.MakeFunc(sig, func(args []reflect.Value) []reflect.Value {
		return f.current.Load().fns[symName].Call(args)
	}).Interface()
	f.symbols[symName] = sym
	return sym, nil
}

// materialize caches the version's own func value for a symbol.
func (f *file) materialize(v *version, symName string) error {
	if _, ok := v.fns[symName]; ok {
		return nil
	}
	fv, err := v.prog.FuncValue(symName)
	if err != nil {
		return fmt.Errorf("plugin: %w", err)
	}
	v.fns[symName] = fv
	return nil
}

// Reload rereads and recompiles the source. Every symbol already
// handed out must still exist with an identical signature, or the
// reload is refused and the old version stays live; on success one
// atomic store swaps the implementation under every held symbol, and
// calls in flight finish on the version they entered with.
func (f *file) Reload() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	next, err := f.load()
	if err != nil {
		return fmt.Errorf("plugin: reload %s: %w", f.path, err)
	}
	cur := f.current.Load()
	for symName := range f.symbols {
		oldSig, _ := cur.prog.Signature(symName)
		newSig, ok := next.prog.Signature(symName)
		if !ok {
			return fmt.Errorf("plugin: reload %s: symbol %s is gone", f.path, symName)
		}
		if newSig != oldSig {
			return fmt.Errorf("plugin: reload %s: symbol %s changed from %s to %s", f.path, symName, oldSig, newSig)
		}
		if err := f.materialize(next, symName); err != nil {
			return err
		}
	}
	f.current.Store(next)
	return nil
}
