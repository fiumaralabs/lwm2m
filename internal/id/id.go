package id

import (
	"crypto/rand"
	"encoding/base32"
	"sync"
	"time"
)

var mu sync.Mutex
var last int64 = -1
var entropy [10]byte
var crockford = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// New returns a prefixed ULID, strictly increasing across calls regardless of clock rollback.
func New(prefix string) string {
	mu.Lock()
	defer mu.Unlock()
	if now := time.Now().UnixMilli(); now > last {
		rand.Read(entropy[:])
		last = now
	} else {
		for i := len(entropy) - 1; i >= 0; i-- {
			entropy[i]++
			if entropy[i] != 0 {
				break
			}
			if i == 0 {
				last++
			}
		}
	}
	var raw [17]byte
	for i := range 6 {
		raw[5-i] = byte(uint64(last) >> (8 * i))
	}
	copy(raw[6:], entropy[:])
	for i := len(raw) - 1; i > 0; i-- {
		raw[i] = raw[i-1]<<6 | raw[i]>>2
	}
	raw[0] >>= 2
	return prefix + "_" + crockford.EncodeToString(raw[:])[:26]
}
