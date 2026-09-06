package divecomputer

/*
#include <wchar.h>
unsigned long dv_load(const wchar_t *path, unsigned long flags);
*/
import "C"

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

// Resolve all native entry points once, before any context or callback is used.
var ensureRuntime = sync.OnceValue(func() error {
	path, err := runtimePath()
	if err != nil {
		return err
	}
	wide, err := syscall.UTF16FromString(path)
	if err != nil {
		return err
	}
	if code := C.dv_load((*C.wchar_t)(unsafe.Pointer(&wide[0])), C.ulong(runtimeLoadFlags)); code != 0 {
		return fmt.Errorf("load libdivecomputer %s: %w", path, syscall.Errno(code))
	}
	return nil
})
