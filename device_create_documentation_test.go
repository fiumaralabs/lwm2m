package lwm2m

import (
	"os"
	"strings"
	"testing"
)

// These assertions guard the documented wire contract; the endpoint is not yet implemented.
func TestDeviceCreateOutcomeDocumentation(t *testing.T) {
	doc, err := os.ReadFile("doc/API.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"A tombstone is retained indefinitely, including after acknowledgement",
		"A PUT for an acknowledged ID returns 409 `operation-id-reused`",
		"`fingerprint` is JSON `null` for a bodyless fence",
		"For `pending`, `fingerprint` is the request fingerprint if known, otherwise JSON `null`",
		"`completedAt` is JSON `null` for `pending`",
	} {
		if !strings.Contains(string(doc), text) {
			t.Errorf("device-create contract missing %q", text)
		}
	}
}
