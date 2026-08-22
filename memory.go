package ffibridge

import (
	"errors"
	"fmt"
	"unsafe"
)

// maxCStringLen bounds GoStringAt so a missing terminator cannot walk forever.
const maxCStringLen = 1 << 20

// Memory handed out by Alloc is a Go byte slice kept alive in the bridge. Host
// operations resolve its address back to that slice; only addresses owned by
// native code are accessed through unsafe.Pointer.

// ownedBlockLocked returns the part of a bridge-owned block starting at addr.
// The caller must hold b.mu while using the returned slice.
func (b *Bridge) ownedBlockLocked(addr uintptr) ([]byte, bool) {
	for base, buf := range b.blocks {
		if addr >= base {
			off := addr - base
			if off < uintptr(len(buf)) {
				return buf[int(off):], true
			}
		}
	}
	return nil, false
}

// foreignBytes converts an address owned by native code to a byte slice. Such
// memory is not owned or moved by the Go garbage collector, but the compiler
// cannot recover that provenance from a uintptr. Invalid addresses may still
// crash, as documented by Peek and Poke.
//
//go:nocheckptr
func foreignBytes(addr uintptr, n int) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(addr)), n)
}

func (b *Bridge) block(addr uintptr) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrClosed
	}
	buf, ok := b.blocks[addr]
	if !ok {
		return nil, fmt.Errorf("ffibridge: %#x is not a block owned by this bridge", addr)
	}
	return buf, nil
}

// Alloc reserves a zeroed block and returns its address.
func (b *Bridge) Alloc(size int) (uintptr, error) {
	if size <= 0 {
		return 0, fmt.Errorf("ffibridge: allocation size must be positive, got %d", size)
	}
	if err := b.allow(OpAlloc, fmt.Sprintf("%d", size)); err != nil {
		return 0, err
	}

	max := b.opts.MaxAlloc
	if max <= 0 {
		max = DefaultMaxAlloc
	}

	buf := make([]byte, size)
	addr := uintptr(unsafe.Pointer(&buf[0]))

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, ErrClosed
	}
	if b.allocated+int64(size) > max {
		return 0, fmt.Errorf("ffibridge: allocation of %d bytes exceeds the %d byte budget", size, max)
	}
	b.blocks[addr] = buf
	b.allocated += int64(size)
	return addr, nil
}

// Free releases a block previously returned by Alloc or CString.
func (b *Bridge) Free(addr uintptr) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	buf, ok := b.blocks[addr]
	if !ok {
		return fmt.Errorf("ffibridge: %#x is not a block owned by this bridge", addr)
	}
	delete(b.blocks, addr)
	b.allocated -= int64(len(buf))
	return nil
}

// Bytes exposes a bridge-owned block for direct host-side access. Writes
// through the returned slice are visible to native code.
func (b *Bridge) Bytes(addr uintptr) ([]byte, error) {
	return b.block(addr)
}

// Write copies data into a bridge-owned block at the given offset.
func (b *Bridge) Write(addr uintptr, off int, data []byte) error {
	buf, err := b.block(addr)
	if err != nil {
		return err
	}
	if off < 0 || off+len(data) > len(buf) {
		return fmt.Errorf("ffibridge: write of %d bytes at offset %d is out of bounds for a %d byte block", len(data), off, len(buf))
	}
	copy(buf[off:], data)
	return nil
}

// Read copies bytes out of a bridge-owned block.
func (b *Bridge) Read(addr uintptr, off, n int) ([]byte, error) {
	buf, err := b.block(addr)
	if err != nil {
		return nil, err
	}
	if off < 0 || n < 0 || off+n > len(buf) {
		return nil, fmt.Errorf("ffibridge: read of %d bytes at offset %d is out of bounds for a %d byte block", n, off, len(buf))
	}
	out := make([]byte, n)
	copy(out, buf[off:off+n])
	return out, nil
}

// CString allocates a NUL terminated copy of s and returns its address.
func (b *Bridge) CString(s string) (uintptr, error) {
	addr, err := b.Alloc(len(s) + 1)
	if err != nil {
		return 0, err
	}
	if err := b.Write(addr, 0, []byte(s)); err != nil {
		_ = b.Free(addr)
		return 0, err
	}
	return addr, nil
}

// Peek reads raw memory at an arbitrary address. It is unchecked by nature:
// a bad address crashes the process, exactly as it would in C.
func (b *Bridge) Peek(addr uintptr, n int) ([]byte, error) {
	if err := b.allow(OpPeek, fmt.Sprintf("%#x+%d", addr, n)); err != nil {
		return nil, err
	}
	if addr == 0 {
		return nil, errors.New("ffibridge: peek at a null address")
	}
	if n < 0 {
		return nil, fmt.Errorf("ffibridge: negative peek length %d", n)
	}
	b.mu.Lock()
	if buf, ok := b.ownedBlockLocked(addr); ok {
		defer b.mu.Unlock()
		if n > len(buf) {
			return nil, fmt.Errorf("ffibridge: peek of %d bytes exceeds the %d bytes remaining in a bridge-owned block", n, len(buf))
		}
		out := make([]byte, n)
		copy(out, buf[:n])
		return out, nil
	}
	b.mu.Unlock()
	out := make([]byte, n)
	copy(out, foreignBytes(addr, n))
	return out, nil
}

// Poke writes raw memory at an arbitrary address.
func (b *Bridge) Poke(addr uintptr, data []byte) error {
	if err := b.allow(OpPoke, fmt.Sprintf("%#x+%d", addr, len(data))); err != nil {
		return err
	}
	if addr == 0 {
		return errors.New("ffibridge: poke at a null address")
	}
	b.mu.Lock()
	if buf, ok := b.ownedBlockLocked(addr); ok {
		defer b.mu.Unlock()
		if len(data) > len(buf) {
			return fmt.Errorf("ffibridge: poke of %d bytes exceeds the %d bytes remaining in a bridge-owned block", len(data), len(buf))
		}
		copy(buf, data)
		return nil
	}
	b.mu.Unlock()
	copy(foreignBytes(addr, len(data)), data)
	return nil
}

// GoStringAt reads a NUL terminated string from an arbitrary address.
func (b *Bridge) GoStringAt(addr uintptr) (string, error) {
	if err := b.allow(OpPeek, fmt.Sprintf("%#x", addr)); err != nil {
		return "", err
	}
	if addr == 0 {
		return "", nil
	}
	b.mu.Lock()
	if buf, ok := b.ownedBlockLocked(addr); ok {
		defer b.mu.Unlock()
		limit := len(buf)
		if limit > maxCStringLen {
			limit = maxCStringLen
		}
		for length := 0; length < limit; length++ {
			if buf[length] == 0 {
				return string(buf[:length]), nil
			}
		}
		if len(buf) < maxCStringLen {
			return "", fmt.Errorf("ffibridge: no NUL terminator before the end of the bridge-owned block at %#x", addr)
		}
		return "", fmt.Errorf("ffibridge: no NUL terminator within %d bytes at %#x", maxCStringLen, addr)
	}
	b.mu.Unlock()
	buf := foreignBytes(addr, maxCStringLen)
	length := 0
	for length < maxCStringLen {
		if buf[length] == 0 {
			break
		}
		length++
	}
	if length >= maxCStringLen {
		return "", fmt.Errorf("ffibridge: no NUL terminator within %d bytes at %#x", maxCStringLen, addr)
	}
	return string(buf[:length]), nil
}
