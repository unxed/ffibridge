//go:build !noffi && (windows || ((linux || android || darwin || freebsd || netbsd) && (amd64 || arm64)))

package ffibridge

import (
	"fmt"
	"reflect"

	purego "github.com/ebitengine/purego"
)

// Supported reports whether this build can make native calls. Building with
// the noffi tag, or on a platform pureffi does not cover, leaves the rest of
// the plugin machinery intact and only disables the escape hatch.
//
// The constraint above tracks the targets pureffi's ffi layer actually has an
// implementation for: Windows on any architecture, and Linux, Android, macOS,
// FreeBSD or NetBSD on amd64/arm64. Excluding only "arm" was not enough --
// that still let linux/386, linux/riscv64 and the mips/ppc64 targets in, where
// ffi does not build at all and the import failed the whole binary rather
// than falling back to the stub below.
const Supported = true

func dlOpen(name string) (uintptr, error) {
	return purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

func dlSym(lib uintptr, name string) (uintptr, error) {
	return purego.Dlsym(lib, name)
}

func dlClose(lib uintptr) error {
	return purego.Dlclose(lib)
}

// makeCallable binds a native address to a Go function type built at run time.
// purego reports every problem by panicking, so the panic is contained here
// and reported as an ordinary error.
func makeCallable(ftype reflect.Type, fn uintptr) (callable reflect.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			callable, err = reflect.Value{}, fmt.Errorf("ffibridge: cannot bind %s: %v", ftype, r)
		}
	}()
	holder := reflect.New(ftype)
	purego.RegisterFunc(holder.Interface(), fn)
	return holder.Elem(), nil
}

func makeCallback(body any) (addr uintptr, err error) {
	defer func() {
		if r := recover(); r != nil {
			addr, err = 0, fmt.Errorf("ffibridge: cannot create callback: %v", r)
		}
	}()
	return purego.NewCallback(body), nil
}
