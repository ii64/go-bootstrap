package stub

import (
	"unsafe"
)

// See https://github.com/golang/go/blob/master/src/cmd/go/internal/load/pkg.go disallowInternal

type DisallowInternalFunc func(srcDir string, p unsafe.Pointer, stk unsafe.Pointer) unsafe.Pointer

//go:noinline
func DisallowInternalReplacer(srcDir string, p unsafe.Pointer, stk unsafe.Pointer) unsafe.Pointer
