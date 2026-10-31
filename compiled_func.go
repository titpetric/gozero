package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// CompiledFunc is the constructed closure a statement compiles to: the
// JIT'd direct call when the binding fits a shape in the table,
// otherwise the reflect path. The result is the callee's return value
// boxed into an any; a pointer result boxes without an allocation.
//
// CompiledFunc is a defined type, and a plain func would leave Exec and
// Scan nowhere to hang as generic methods.
type CompiledFunc func(ctx context.Context, stack map[string]any, dest any) (any, error)

// Exec runs a compiled statement against a stack and returns the result as T.
func (fn CompiledFunc) Exec[T any](stack map[string]any) (T, error) {
	return fn.ExecContext[T](context.Background(), stack)
}

// ExecContext is Exec with an execution context. A binding parameter of
// type context.Context the program does not pass explicitly receives
// this context in place of a zero value.
func (fn CompiledFunc) ExecContext[T any](ctx context.Context, stack map[string]any) (T, error) {
	var zero T
	out, err := fn(ctx, stack, nil)
	if err != nil || out == nil {
		return zero, err
	}
	v, ok := out.(T)
	if !ok {
		return zero, fmt.Errorf("exec: result is %T, want %T", out, zero)
	}
	return v, nil
}

// Scan runs a compiled program with dest bound to the name "dest" and, when
// the program has a value, copies it into dest, dereferencing a *T result.
func (fn CompiledFunc) Scan[T any](dest *T, stack map[string]any) error {
	if dest == nil {
		return fmt.Errorf("scan: dest must be a non-nil *%T", *new(T))
	}
	return scanInto(dest, fn, context.Background(), stack)
}

// ScanContext is Scan with an execution context; see ExecContext.
func (fn CompiledFunc) ScanContext[T any](ctx context.Context, dest *T, stack map[string]any) error {
	if dest == nil {
		return fmt.Errorf("scan: dest must be a non-nil *%T", *new(T))
	}
	return scanInto(dest, fn, ctx, stack)
}

func scanInto(dest any, fn CompiledFunc, ctx context.Context, stack map[string]any) error {
	res, err := fn(ctx, stack, dest)
	if err != nil {
		return err
	}
	if res == nil {
		return nil
	}
	de := reflect.ValueOf(dest).Elem()
	out := reflect.ValueOf(res)
	switch {
	case out.Type().AssignableTo(de.Type()):
		de.Set(out)
	case out.Kind() == reflect.Pointer && out.Type().Elem() == de.Type():
		if out.IsNil() {
			de.SetZero()
		} else {
			de.Set(out.Elem())
		}
	default:
		return fmt.Errorf("scan: cannot scan %s into %s", out.Type(), de.Type())
	}
	return nil
}
