package apiv1

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

// Run the real handler with an extra secured route, injected only at compile time.
func TestOpenAPIGateUndocumentedRoute(t *testing.T) {
	if os.Getenv("LWM2M_DRIFT_OVERLAY") == "1" {
		keys, err := LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x admin", sha256.Sum256([]byte("lwm_test_secret")))))
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			method, path string
			status       int
		}{
			{http.MethodGet, "/v1/drift-probe", http.StatusNotFound},
			{http.MethodGet, "/v1/devices/probe%2Fundocumented", http.StatusNotFound},
			{http.MethodPost, "/v1/info", http.StatusMethodNotAllowed},
		} {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer lwm_test_secret")
			w := httptest.NewRecorder()
			New(Options{Keys: keys}).ServeHTTP(w, req)
			if w.Code != tc.status || w.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("%s %s: undocumented handler reached: status %d, content type %q", tc.method, tc.path, w.Code, w.Header().Get("Content-Type"))
			}
		}
		devices := &memory.Devices{}
		if _, err := devices.Create("default", store.Device{Endpoint: "probe/undocumented"}); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/v1/devices/probe%2Fundocumented", nil)
		req.Header.Set("Authorization", "Bearer lwm_test_secret")
		w := httptest.NewRecorder()
		New(Options{Keys: keys, Devices: devices}).ServeHTTP(w, req)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"endpoint":"probe/undocumented"`) {
			t.Fatalf("encoded endpoint ID not preserved: status %d, body %s", w.Code, w.Body.String())
		}
		return
	}
	source, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	marker := "\t\tif strings.HasPrefix(r.URL.Path, \"/v1/device-creates/\") {"
	if strings.Count(string(source), marker) != 1 {
		t.Fatal("router insertion marker changed")
	}
	injection := "		if (r.URL.Path == \"/v1/drift-probe\" || r.URL.Path == \"/v1/devices/probe/undocumented\") && r.Method == http.MethodGet || r.URL.Path == \"/v1/info\" && r.Method == http.MethodPost { w.WriteHeader(http.StatusNoContent); return }\n"
	dir := t.TempDir()
	replacement := filepath.Join(dir, "router.go")
	if err := os.WriteFile(replacement, []byte(strings.Replace(string(source), marker, injection+marker, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	original, err := filepath.Abs("router.go")
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{original: replacement}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "-overlay", overlayPath, "-run", "^TestOpenAPIGateUndocumentedRoute$", "-count=1", ".")
	cmd.Env = append(os.Environ(), "LWM2M_DRIFT_OVERLAY=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("overlay regression: %v\n%s", err, output)
	}
}
