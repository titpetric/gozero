// Command main runs a gozero program from a file against a stdlib
// binding set:
//
//	go run testdata/main.go testdata/url.txt
//
// A failed assertion or a failing call ends the program through the
// implicit error contract and exits non-zero; a receive on a closed
// channel ends it with io.EOF.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"reflect"
	"strings"

	"github.com/titpetric/gozero"
)

func main() {
	if err := start(); err != nil {
		log.Fatal(err)
	}
}

type ctxKey struct{}

func start() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: %s <program.txt>", os.Args[0])
	}
	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		return err
	}

	rt := gozero.NewRuntime()
	for name, fn := range bindings() {
		if err := rt.Bind(name, fn); err != nil {
			return err
		}
	}
	fn, err := rt.Compile(string(src))
	if err != nil {
		return err
	}

	ctx := context.WithValue(context.Background(), ctxKey{}, "fixture")
	_, err = fn.ExecContext[any](ctx, nil)
	return err
}

// bindings is the surface the program can reach: the sandbox boundary.
// The assert functions return an error, so a failed assertion ends the
// program the way any failing call does.
func bindings() map[string]any {
	return map[string]any{
		"http.NewRequest":            http.NewRequest,
		"http.NewRequestWithContext": http.NewRequestWithContext,
		"url.Parse":                  url.Parse,
		"url.ParseQuery":             url.ParseQuery,
		"json.NewEncoder":            json.NewEncoder,
		"json.Marshal":               json.Marshal,
		"bytes.NewBufferString":      bytes.NewBufferString,
		"fmt.Sprintf":                fmt.Sprintf,
		"fmt.Sprint":                 fmt.Sprint,
		"fmt.Println":                fmt.Println,
		"strings.Fields":             strings.Fields,
		"path.Join":                  path.Join,
		"gozero.NewMutexMap":         gozero.NewMutexMap,
		"chanOf": func(vs ...string) chan string {
			c := make(chan string, len(vs)+2)
			for _, v := range vs {
				c <- v
			}
			return c
		},
		"ctxValue": func(ctx context.Context) string {
			v, _ := ctx.Value(ctxKey{}).(string)
			return v
		},
		"assert.Equal": func(tb any, want, got any, msg string) error {
			if !reflect.DeepEqual(want, got) {
				return fmt.Errorf("assert: got %v (%T), want %v (%T) %s", got, got, want, want, msg)
			}
			return nil
		},
		"assert.True": func(tb any, ok bool, msg string) error {
			if !ok {
				return fmt.Errorf("assert: not true %s", msg)
			}
			return nil
		},
	}
}
