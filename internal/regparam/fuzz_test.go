package regparam

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func FuzzParseRequest(f *testing.F) {
	for _, s := range []string{"POST /rd?ep=node&lt=86400&lwm2m=1.1&b=U&Q", "POST /rd?a&a", "POST rd", "GET /", "POST /rd?Q="} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		a, err := ParseRequest(line)
		if err != nil {
			return
		}
		// Query options are unordered. Reversing them preserves the request.
		method, target, _ := strings.Cut(line, " ")
		path, query, has := strings.Cut(target, "?")
		if has {
			opts := strings.Split(query, "&")
			slices.Reverse(opts)
			line = method + " " + path + "?" + strings.Join(opts, "&")
		}
		b, err := ParseRequest(line)
		if err != nil || !reflect.DeepEqual(a, b) {
			t.Fatalf("reordered request: %#v != %#v: %v", a, b, err)
		}
	})
}

func FuzzParseBinding(f *testing.F) {
	for _, s := range [][2]string{{"UQ", "1.0"}, {"UQS", "1.0"}, {"UTSN", "1.1"}, {"MH", "1.2.1"}, {"UQSQ", "1.0"}, {"M", "1.1"}, {"", "1.1"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, input, version string) {
		b, err := ParseBinding(input, version)
		if err != nil {
			return
		}
		if b.Transports == "" {
			t.Fatal("empty transports")
		}
		seen := ""
		for _, c := range b.Transports {
			if !strings.ContainsRune("UTSNMH", c) || strings.ContainsRune(seen, c) {
				t.Fatalf("invalid transports: %#v", b)
			}
			seen += string(c)
		}
		// UQS is the legacy spelling for queued US, not USQ.
		formatted := b.Transports
		if b.Queue {
			if formatted == "US" {
				formatted = "UQS"
			} else {
				formatted += "Q"
			}
		}
		again, err := ParseBinding(formatted, version)
		if err != nil || b != again {
			t.Fatalf("binding %q -> %q: %#v != %#v: %v", input, formatted, b, again, err)
		}
	})
}

func FuzzParseProfileIDs(f *testing.F) {
	for _, s := range []string{"6:ab", `"6:1234abcd,oma:dev-1,v:x"`, "oma:a_b", "256:ab", `"6:ab,,v:x"`, ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		ids, err := ParseProfileIDs(input)
		if err != nil {
			return
		}
		parts := make([]string, 0, len(ids))
		for _, id := range ids {
			switch id.Kind {
			case "dynamic":
				parts = append(parts, strconv.Itoa(int(id.Suite))+":"+id.Hash)
			case "oma", "v":
				parts = append(parts, id.Kind+":"+id.Value)
			default:
				t.Fatalf("unknown profile kind: %#v", id)
			}
		}
		formatted := strings.Join(parts, ",")
		if len(parts) > 1 {
			formatted = `"` + formatted + `"`
		}
		again, err := ParseProfileIDs(formatted)
		if err != nil || !reflect.DeepEqual(ids, again) {
			t.Fatalf("profile %q -> %q: %#v != %#v: %v", input, formatted, ids, again, err)
		}
	})
}

func FuzzParseRegister(f *testing.F) {
	for _, s := range []string{"lwm2m=1.1&ep=node&lt=86400&b=U&Q&sms=12345678&pid=\"6:1234abcd,oma:dev-1,v:x\"", "ep=a&lt=60&lwm2m=1.0&b=UQ", "lt=60&lwm2m=1.2&Q=", "ep=a&lt=-1&lwm2m=1.1", "ep=a&lt=60&lt=61&lwm2m=1.1", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, query string) {
		opts := strings.Split(query, "&")
		a, err := ParseRegister(opts)
		if err != nil {
			return
		}
		if a.Lifetime == nil || a.Version == nil || *a.Version == "" {
			t.Fatalf("missing required fields: %#v", a)
		}
		if minor(*a.Version) == "1.0" && a.Endpoint == nil {
			t.Fatalf("1.0 missing endpoint: %#v", a)
		}
		slices.Reverse(opts)
		b, err := ParseRegister(opts)
		if err != nil || !reflect.DeepEqual(a, b) {
			t.Fatalf("reordered register: %#v != %#v: %v", a, b, err)
		}
	})
}

func FuzzParseUpdate(f *testing.F) {
	for _, s := range [][2]string{{"", "1.1"}, {"lt=600000&b=UQ&pid=6:ab", "1.0"}, {"ep=x", "1.1"}, {"lwm2m=1.1", "1.1"}, {"lt=x", "1.1"}, {"lt=4294967295&Q", "1.2"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, query, version string) {
		var opts []string
		if query != "" {
			opts = strings.Split(query, "&")
		}
		a, err := ParseUpdate(opts, version)
		if err != nil {
			return
		}
		if a.Endpoint != nil || a.Version != nil {
			t.Fatalf("update includes register-only fields: %#v", a)
		}
		slices.Reverse(opts)
		b, err := ParseUpdate(opts, version)
		if err != nil || !reflect.DeepEqual(a, b) {
			t.Fatalf("reordered update: %#v != %#v: %v", a, b, err)
		}
	})
}
