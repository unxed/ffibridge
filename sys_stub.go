//go:build noffi || (!windows && (!(linux || android || darwin || freebsd) || (!amd64 && !arm64)))

package ffibridge

import "reflect"

// Supported reports whether this build can make native calls.
const Supported = false

func dlOpen(string) (uintptr, error) { return 0, ErrUnsupported }

func dlSym(uintptr, string) (uintptr, error) { return 0, ErrUnsupported }

func dlClose(uintptr) error { return nil }

func makeCallable(reflect.Type, uintptr) (reflect.Value, error) {
	return reflect.Value{}, ErrUnsupported
}

func makeCallback(any) (uintptr, error) { return 0, ErrUnsupported }
