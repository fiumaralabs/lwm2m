package operations

import (
	"encoding/json"
	"testing"
)

func TestDiscoverLinksAttributes(t *testing.T) {
	links, err := discoverLinks([]byte(`</3>;ver="1.2",</3/0>;pmax=60;rt="sensor, room",</3/0/7>;dim=2`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(links)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"path":"/3","attributes":{"ver":"1.2"}},{"path":"/3/0","attributes":{"pmax":60,"rt":"sensor, room"}},{"path":"/3/0/7","dim":2}]`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	for _, bad := range []string{`</3>;pmax=no`, `</3`, `</3>;dim`} {
		if _, err := discoverLinks([]byte(bad)); err == nil {
			t.Errorf("accepted malformed link %q", bad)
		}
	}
}
