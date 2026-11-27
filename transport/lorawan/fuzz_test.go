package lorawan

import (
	"fmt"
	"testing"
)

func FuzzParseServerURI(f *testing.F) {
	for _, s := range []string{"lorawan://1", "lorawan://255", "lorawan://0", "lorawan://256", "coap://1", "lorawan://+5", "lorawan://"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		port, err := ParseServerURI(s)
		if err != nil {
			return
		}
		if port == 0 {
			t.Fatal("zero port accepted")
		}
		again, err := ParseServerURI(fmt.Sprintf("lorawan://%d", port))
		if err != nil || again != port {
			t.Fatalf("canonical port reparse = %d, %v; want %d", again, err, port)
		}
	})
}
