package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConfigUnknownKeyAtStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"not-a-flag": 1}`), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", ".", "-config", path)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "not-a-flag") {
		t.Fatalf("expected unknown key error, got %v: %s", err, out)
	}
}

func TestConfigPrecedence(t *testing.T) {
	tests := []struct {
		name, contents string
		args           []string
		want           map[string]string
		err            string
	}{
		{name: "defaults", want: map[string]string{"coap": ":5683", "metrics": "true", "cid": "6"}},
		{name: "file only", contents: `{"coap":"","coaps":"a","tcp":"b","http":"c","metrics":false,"timeseries":false,"store":"sqlite:x","prefix":"/x","api-keys":"k","bs-coap":"d","bs-coaps":"e","bs-http":"f","cid":0,"fw-dir":"g","fw-coap":"h","fw-coaps":"i","v":true}`, want: map[string]string{"coap": "", "coaps": "a", "tcp": "b", "http": "c", "metrics": "false", "timeseries": "false", "store": "sqlite:x", "prefix": "/x", "api-keys": "k", "bs-coap": "d", "bs-coaps": "e", "bs-http": "f", "cid": "0", "fw-dir": "g", "fw-coap": "h", "fw-coaps": "i", "v": "true"}},
		{name: "explicit flags override including defaults", contents: `{"coap":"file","metrics":false,"cid":0,"prefix":"/file"}`, args: []string{"-coap=:5683", "-metrics=true", "-cid=6"}, want: map[string]string{"coap": ":5683", "metrics": "true", "cid": "6", "prefix": "/file"}},
		{name: "unknown key", contents: `{"bad-key":true}`, err: "bad-key"},
		{name: "wrong type", contents: `{"cid":"six"}`, err: "cid"},
		{name: "null is not a flag value", contents: `{"metrics":null}`, err: "metrics"},
		{name: "null is not an object", contents: `null`, err: "object"},
		{name: "missing file", contents: "missing", err: "no such file"},
		{name: "trailing data", contents: `{} {}`, err: "multiple JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			defaults := map[string]string{"coap": ":5683", "coaps": ":5684", "tcp": "", "http": ":8080", "store": "memory", "prefix": "/api", "api-keys": "", "bs-coap": ":5783", "bs-coaps": ":5784", "bs-http": ":8081", "fw-dir": "", "fw-coap": ":5693", "fw-coaps": ":5694"}
			for name, value := range defaults {
				fs.String(name, value, "")
			}
			fs.Bool("metrics", true, "")
			fs.Bool("timeseries", true, "")
			fs.Bool("v", false, "")
			fs.Int("cid", 6, "")
			if err := fs.Parse(tt.args); err != nil {
				t.Fatal(err)
			}
			var err error
			if tt.contents != "" {
				path := filepath.Join(t.TempDir(), "config.json")
				if tt.contents != "missing" {
					if err := os.WriteFile(path, []byte(tt.contents), 0600); err != nil {
						t.Fatal(err)
					}
				}
				err = applyConfig(fs, path)
			}
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := make(map[string]string)
			for name := range tt.want {
				got[name] = fs.Lookup(name).Value.String()
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
