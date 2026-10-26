package id

import (
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	before := time.Now().UnixMilli()
	first, next := New("op"), New("op")
	if len(first) != 29 || !strings.HasPrefix(first, "op_") || first >= next {
		t.Fatalf("invalid or unordered IDs: %q, %q", first, next)
	}
	value := new(big.Int)
	for _, c := range first[3:] {
		n := strings.IndexRune(alphabet, c)
		if n < 0 {
			t.Fatalf("invalid Crockford character: %c", c)
		}
		value.Lsh(value, 5).Or(value, big.NewInt(int64(n)))
	}
	stamp := value.Rsh(value, 80).Int64()
	if stamp < before || stamp > time.Now().UnixMilli() {
		t.Fatalf("invalid timestamp: %d", stamp)
	}
	mu.Lock()
	savedLast, savedEntropy := last, entropy
	defer func() { last, entropy = savedLast, savedEntropy }()
	last += 3600000
	entropy = [10]byte{}
	mu.Unlock()
	first, next = New(""), New("")
	if first >= next || !strings.HasSuffix(first, "0000000000000001") {
		t.Fatalf("non-monotonic entropy during rollback: %q, %q", first, next)
	}
	var wg sync.WaitGroup
	var seen sync.Map
	for range 10 {
		wg.Go(func() {
			for range 100 {
				if _, loaded := seen.LoadOrStore(New("op"), true); loaded {
					t.Error("duplicate ID")
				}
			}
		})
	}
	wg.Wait()
}
