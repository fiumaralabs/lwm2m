package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonSQLiteRegistrationsRequireSQLite(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "lwm2md")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	out, err := exec.Command(binary, "-store=memory", "-registration-store=sqlite").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "-registration-store=sqlite requires -store=sqlite:<path>") {
		t.Fatalf("expected configuration error, got %v: %s", err, out)
	}
}
