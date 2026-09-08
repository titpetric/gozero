package gozero

import (
	"context"
	"reflect"
	"unsafe" // also required by go:linkname
)

// sliceHdr mirrors the three words of a slice.
type sliceHdr struct {
	ptr      unsafe.Pointer
	len, cap int
}

// layout is the ABI shape of a value: what the callee reads, ignoring
// the type's name.
type layout int

// scalar reports whether a class travels through the node tree as a
// machine word rather than as a pointer, string, interface or slice.
func (l layout) scalar() bool { return l >= lBool }

// float reports whether a scalar class is carried as a float64.
func (l layout) float() bool { return l == lF32 || l == lF64 }

func layoutOf(t reflect.Type) layout {
	switch t.Kind() {
	case reflect.Pointer, reflect.UnsafePointer, reflect.Map, reflect.Chan, reflect.Func:
		return lPtr
	case reflect.String:
		return lStr
	case reflect.Interface:
		return lIface
	case reflect.Slice:
		return lSlice
	case reflect.Bool:
		return lBool
	case reflect.Int8:
		return lI8
	case reflect.Int16:
		return lI16
	case reflect.Int32:
		return lI32
	case reflect.Int64:
		return lI64
	case reflect.Uint8:
		return lU8
	case reflect.Uint16:
		return lU16
	case reflect.Uint32:
		return lU32
	case reflect.Uint64:
		return lU64
	case reflect.Float32:
		return lF32
	case reflect.Float64:
		return lF64
	case reflect.Int:
		// int and uint are int64 and uint64 on a 64 bit platform and
		// int32 and uint32 on a 32 bit one, so the class follows the
		// size rather than the kind.
		if t.Size() == 8 {
			return lI64
		}
		return lI32
	case reflect.Uint, reflect.Uintptr:
		if t.Size() == 8 {
			return lU64
		}
		return lU32
	}
	return lBad
}

func (l layout) String() string {
	switch l {
	case lPtr:
		return "P"
	case lStr:
		return "S"
	case lIface:
		return "I"
	case lSlice:
		return "L"
	case lErr:
		return "E"
	case lNone:
		return ""
	case lBool:
		return "b"
	case lI8:
		return "i8"
	case lI16:
		return "i16"
	case lI32:
		return "i32"
	case lI64:
		return "i64"
	case lU8:
		return "u8"
	case lU16:
		return "u16"
	case lU32:
		return "u32"
	case lU64:
		return "u64"
	case lF32:
		return "f32"
	case lF64:
		return "f64"
	}
	return "?"
}

// loadN reads a scalar of class cl out of the frame as raw bits.
func loadN(at unsafe.Pointer, cl layout) uint64 {
	switch cl {
	case lBool:
		if *(*bool)(at) {
			return 1
		}
		return 0
	case lI8:
		return uint64(uint8(*(*int8)(at)))
	case lI16:
		return uint64(uint16(*(*int16)(at)))
	case lI32:
		return uint64(uint32(*(*int32)(at)))
	case lU8:
		return uint64(*(*uint8)(at))
	case lU16:
		return uint64(*(*uint16)(at))
	case lU32:
		return uint64(*(*uint32)(at))
	}
	return *(*uint64)(at) // lI64, lU64
}

func storeN(cl layout, at unsafe.Pointer, v uint64) {
	switch cl {
	case lBool:
		*(*bool)(at) = v != 0
	case lI8:
		*(*int8)(at) = int8(uint8(v))
	case lI16:
		*(*int16)(at) = int16(uint16(v))
	case lI32:
		*(*int32)(at) = int32(uint32(v))
	case lU8:
		*(*uint8)(at) = uint8(v)
	case lU16:
		*(*uint16)(at) = uint16(v)
	case lU32:
		*(*uint32)(at) = uint32(v)
	default:
		*(*uint64)(at) = v
	}
}

func loadF(at unsafe.Pointer, cl layout) float64 {
	if cl == lF32 {
		return float64(*(*float32)(at))
	}
	return *(*float64)(at)
}

func storeF(cl layout, at unsafe.Pointer, v float64) {
	if cl == lF32 {
		*(*float32)(at) = float32(v)
		return
	}
	*(*float64)(at) = v
}

// scalarBits turns a compile-time constant into the representation
// nodeN and nodeF carry.
func scalarBits(v reflect.Value, cl layout) (uint64, float64) {
	switch cl {
	case lBool:
		if v.Bool() {
			return 1, 0
		}
		return 0, 0
	case lI8, lI16, lI32, lI64:
		return uint64(v.Int()), 0
	case lU8, lU16, lU32, lU64:
		return v.Uint(), 0
	}
	return 0, v.Float()
}

func slotNode(cl layout, off uintptr) node {
	if cl.scalar() {
		if cl.float() {
			return node{class: cl, F: func(fr unsafe.Pointer, ctx context.Context, _ map[string]any, _ any) (float64, error) {
				return loadF(unsafe.Add(fr, off), cl), nil
			}}
		}
		return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, _ map[string]any, _ any) (uint64, error) {
			return loadN(unsafe.Add(fr, off), cl), nil
		}}
	}
	switch cl {
	case lPtr:
		return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, _ map[string]any, _ any) (unsafe.Pointer, error) {
			return *(*unsafe.Pointer)(unsafe.Add(fr, off)), nil
		}}
	case lSlice:
		return node{class: lSlice, L: func(fr unsafe.Pointer, ctx context.Context, _ map[string]any, _ any) (sliceHdr, error) {
			return *(*sliceHdr)(unsafe.Add(fr, off)), nil
		}}
	case lStr:
		return node{class: lStr, S: func(fr unsafe.Pointer, ctx context.Context, _ map[string]any, _ any) (string, error) {
			return *(*string)(unsafe.Add(fr, off)), nil
		}}
	default:
		return node{class: lIface, I: func(fr unsafe.Pointer, ctx context.Context, _ map[string]any, _ any) (ifacePair, error) {
			return *(*ifacePair)(unsafe.Add(fr, off)), nil
		}}
	}
}
