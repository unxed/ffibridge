package ffibridge

import (
	"errors"
	"testing"
)

func TestAllocWriteRead(t *testing.T) {
	b := New(Options{})
	defer b.Close()

	addr, err := b.Alloc(16)
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if addr == 0 {
		t.Fatal("Alloc returned a null address")
	}

	if err := b.Write(addr, 4, []byte("data")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := b.Read(addr, 4, 4)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != "data" {
		t.Fatalf("Read = %q, want \"data\"", got)
	}

	if err := b.Write(addr, 14, []byte("overflow")); err == nil {
		t.Error("Write past the end of a block was allowed")
	}
	if _, err := b.Read(addr, 0, 32); err == nil {
		t.Error("Read past the end of a block was allowed")
	}
}

func TestCStringRoundTrip(t *testing.T) {
	b := New(Options{})
	defer b.Close()

	addr, err := b.CString("hello")
	if err != nil {
		t.Fatalf("CString: %v", err)
	}
	got, err := b.GoStringAt(addr)
	if err != nil {
		t.Fatalf("GoStringAt: %v", err)
	}
	if got != "hello" {
		t.Fatalf("GoStringAt = %q, want \"hello\"", got)
	}

	if empty, err := b.GoStringAt(0); err != nil || empty != "" {
		t.Fatalf("GoStringAt(0) = %q, %v; want \"\", nil", empty, err)
	}
}

func TestPeekPoke(t *testing.T) {
	b := New(Options{})
	defer b.Close()

	addr, err := b.Alloc(12)
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	addr += 4
	if err := b.Poke(addr, []byte("hello\x00")); err != nil {
		t.Fatalf("Poke: %v", err)
	}
	got, err := b.Peek(addr, 5)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("Peek = %q, want \"hello\"", got)
	}
	str, err := b.GoStringAt(addr)
	if err != nil {
		t.Fatalf("GoStringAt: %v", err)
	}
	if str != "hello" {
		t.Fatalf("GoStringAt = %q, want \"hello\"", str)
	}
	if _, err := b.Peek(addr, 9); err == nil {
		t.Error("Peek past the end of a bridge-owned block was allowed")
	}
	if err := b.Poke(addr, make([]byte, 9)); err == nil {
		t.Error("Poke past the end of a bridge-owned block was allowed")
	}
	if _, err := b.Peek(0, 4); err == nil {
		t.Error("Peek at a null address was allowed")
	}
}

func TestGoStringAtStopsAtOwnedBlockEnd(t *testing.T) {
	b := New(Options{})
	defer b.Close()

	addr, err := b.Alloc(4)
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if err := b.Poke(addr, []byte("text")); err != nil {
		t.Fatalf("Poke: %v", err)
	}
	if _, err := b.GoStringAt(addr + 2); err == nil {
		t.Error("GoStringAt read past the end of a bridge-owned block")
	}
}

func TestFreeAndBudget(t *testing.T) {
	b := New(Options{MaxAlloc: 32})
	defer b.Close()

	addr, err := b.Alloc(32)
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if _, err := b.Alloc(1); err == nil {
		t.Error("allocation past the budget was allowed")
	}
	if err := b.Free(addr); err != nil {
		t.Fatalf("Free: %v", err)
	}
	if err := b.Free(addr); err == nil {
		t.Error("double free was allowed")
	}
	if _, err := b.Alloc(32); err != nil {
		t.Errorf("budget was not released by Free: %v", err)
	}
}

func TestClosedBridgeRejectsMemory(t *testing.T) {
	b := New(Options{})
	addr, err := b.Alloc(8)
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := b.Read(addr, 0, 1); err == nil {
		t.Error("a closed bridge still served its blocks")
	}
	if _, err := b.Alloc(8); !errors.Is(err, ErrClosed) {
		t.Errorf("Alloc after Close = %v, want ErrClosed", err)
	}
}

func TestPermissionHookBlocksAllocation(t *testing.T) {
	denied := errors.New("denied by policy")
	var seen []Op

	b := New(Options{Allow: func(op Op, detail string) error {
		seen = append(seen, op)
		if op == OpAlloc {
			return denied
		}
		return nil
	}})
	defer b.Close()

	if _, err := b.Alloc(8); !errors.Is(err, denied) {
		t.Fatalf("Alloc = %v, want the policy error", err)
	}
	if len(seen) != 1 || seen[0] != OpAlloc {
		t.Fatalf("permission hook saw %v, want one OpAlloc", seen)
	}
}
