package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdmissionFlagsAndFileFailFast(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "lwm2md")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	tests := []struct {
		name   string
		args   []string
		config string
		want   string
	}{
		{"flags", []string{"-admission-url=https://example.com/v1/admission-decisions"}, "", "admission requires -store=sqlite"},
		{"config", nil, `{"admission-url":"https://example.com/v1/admission-decisions"}`, "admission requires -store=sqlite"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.args
			if tt.config != "" {
				p := filepath.Join(t.TempDir(), "settings.json")
				if err := os.WriteFile(p, []byte(tt.config), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "-config", p)
			}
			out, err := exec.Command(binary, args...).CombinedOutput()
			if err == nil || !strings.Contains(string(out), tt.want) {
				t.Fatalf("got %v: %s; want %s", err, out, tt.want)
			}
		})
	}
}
