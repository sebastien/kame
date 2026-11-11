package c

import (
	"reflect"
	"unsafe"
)

type Int = int32

func PtrAs[T any](p any) *T {
	if p == nil {
		return nil
	}
	return (*T)(unsafe.Pointer(reflect.ValueOf(p).Pointer()))
}
func PtrAdd[T any](p *T, n int) *T {
	return (*T)(unsafe.Add(unsafe.Pointer(p), uintptr(n)*unsafe.Sizeof(*p)))
}
func Bytes(p *byte, n int) []byte { return unsafe.Slice(p, n) }
