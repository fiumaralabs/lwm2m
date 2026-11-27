package lwm2m_test

import (
	"testing"

	lwm2m "github.com/fiumaralabs/lwm2m"
)

func FuzzParsePath(f *testing.F) {
	for _, seed := range []string{"", "/", "3", "/3/0/1/2", "/0/65534", "/65535", "//3", "/3/", "/01/002", "/1/2/3/4/5"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		path, err := lwm2m.ParsePath(input)
		if err != nil {
			return
		}
		again, err := lwm2m.ParsePath(path.String())
		if err != nil || again != path {
			t.Fatalf("round trip %q: %v, %v; want %v", input, again, err, path)
		}
	})
}

func FuzzParsePrefixedPath(f *testing.F) {
	for _, seed := range []string{"", "/", "/3/0", "d01/3303/0", "/d01/3/0/1/2", "/d01", "/123/3", "/x/65535", "/x//3", "/x?y/3"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		prefix, path, err := lwm2m.ParsePrefixedPath(input)
		if err != nil {
			return
		}
		againPrefix, againPath, err := lwm2m.ParsePrefixedPath(lwm2m.PrefixedPath(prefix, path))
		if err != nil || againPrefix != prefix || againPath != path {
			t.Fatalf("round trip %q: %q %v %v; want %q %v", input, againPrefix, againPath, err, prefix, path)
		}
	})
}
