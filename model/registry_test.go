package model

import (
	"io/fs"
	"testing"
)

func TestDefaultAllocationBudget(t *testing.T) {
	Default() // Exclude one-time initialization.
	allocs := testing.AllocsPerRun(3, func() { Default() })
	if allocs > 10000 {
		t.Fatalf("Default allocated %.0f times per call; embedded XML should not be reparsed", allocs)
	}
}

func TestDefaultRegistriesIndependent(t *testing.T) {
	a, b := Default(), Default()
	before, ok := b.Get(3, V1_0)
	if !ok {
		t.Fatal("missing embedded device object")
	}
	doc, err := fs.ReadFile(CoreFS(), "registry/3-1_0.xml")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := a.Register(doc)
	if err != nil {
		t.Fatal(err)
	}
	after, ok := b.Get(3, V1_0)
	if !ok || before != after || after == registered[0] {
		t.Fatal("registering in one default changed another")
	}
	c := Default()
	third, ok := c.Get(3, V1_0)
	if !ok || third == registered[0] {
		t.Fatal("registration affected a later default")
	}
}

func BenchmarkDefault(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Default()
	}
}
