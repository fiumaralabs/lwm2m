package sms

import (
	"testing"
	"time"
)

func TestReassemblyBoundsPendingSets(t *testing.T) {
	b := New(Config{})
	for i := 0; i < 1025; i++ {
		b.reassemble(string(rune(i+1)), SMS{UDH: []byte{0, 3, 1, 2, 1}, Data: []byte("first")})
	}
	if len(b.parts) > 1024 {
		t.Fatalf("pending sets = %d, want at most 1024", len(b.parts))
	}
}

func concatPart(ref, seq byte, data string) SMS {
	return SMS{UDH: []byte{0, 3, ref, 2, seq}, Data: []byte(data)}
}

func TestReassemblyExpiryAndLatePart(t *testing.T) {
	now := time.Unix(100, 0)
	b := New(Config{ConcatTimeout: time.Minute})
	b.now = func() time.Time { return now }
	if _, done := b.reassemble("123", concatPart(1, 1, "old")); done {
		t.Fatal("completed early")
	}
	now = now.Add(time.Minute - time.Second)
	if _, done := b.reassemble("123", concatPart(1, 1, "old")); done {
		t.Fatal("completed early")
	}
	now = now.Add(time.Second)
	if _, done := b.reassemble("123", concatPart(1, 2, "late")); done {
		t.Fatal("expired part was reused")
	}
	out, done := b.reassemble("123", concatPart(1, 1, "new"))
	if !done || string(out.Data) != "newlate" {
		t.Fatalf("got %q, done=%v", out.Data, done)
	}
}

func TestReassemblyEvictsOldest(t *testing.T) {
	now := time.Unix(100, 0)
	b := New(Config{ConcatTimeout: time.Hour, MaxPendingConcatSets: 2})
	b.now = func() time.Time { return now }
	b.reassemble("123", concatPart(1, 1, "old"))
	b.reassemble("123", concatPart(2, 1, "middle"))
	b.reassemble("123", concatPart(3, 1, "new"))
	if _, done := b.reassemble("123", concatPart(1, 2, "late")); done {
		t.Fatal("oldest set was not evicted")
	}
	out, done := b.reassemble("123", concatPart(3, 2, "est"))
	if !done || string(out.Data) != "newest" {
		t.Fatalf("got %q, done=%v", out.Data, done)
	}
}

func TestReassemblyBeforeExpiry(t *testing.T) {
	now := time.Unix(100, 0)
	b := New(Config{ConcatTimeout: time.Minute})
	b.now = func() time.Time { return now }
	b.reassemble("123", concatPart(1, 2, "second"))
	now = now.Add(time.Minute - time.Nanosecond)
	out, done := b.reassemble("123", concatPart(1, 1, "first"))
	if !done || string(out.Data) != "firstsecond" {
		t.Fatalf("got %q, done=%v", out.Data, done)
	}
}
