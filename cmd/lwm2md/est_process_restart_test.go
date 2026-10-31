package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestESTProcessSQLiteRestart(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lwm2md")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	const token = "lwm_est_synthetic"
	keys := filepath.Join(dir, "keys")
	if err := os.WriteFile(keys, []byte(fmt.Sprintf("key_est sha256:%x credentials:write", sha256.Sum256([]byte(token)))), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	start := func() *exec.Cmd {
		t.Helper()
		cmd := exec.Command(binary, "-store", "sqlite:"+filepath.Join(dir, "daemon.db"), "-http", addr, "-api-keys", keys, "-coap=", "-coaps=", "-bs-coap=", "-bs-coaps=", "-bs-http=")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			res, err := client.Get("http://" + addr + "/readyz")
			if err == nil {
				res.Body.Close()
				if res.StatusCode == http.StatusOK {
					return cmd
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("daemon did not become ready")
		return nil
	}
	call := func(method, path string, data []byte) []byte {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+addr+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		want := http.StatusOK
		if method == http.MethodPut {
			want = http.StatusCreated
		}
		if res.StatusCode != want {
			t.Fatalf("%s %s: status %d", method, path, res.StatusCode)
		}
		return body
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	body, err := json.Marshal(map[string]any{"enabled": true, "issuer": map[string]string{"certificate": certPEM, "privateKey": keyPEM}, "validityDays": 30, "selector": "env=prod"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := start()
	call("PUT", "/v1/est", body)
	before := call("GET", "/v1/est", nil)
	anchors := call("GET", "/v1/trust-anchors", nil)
	var anchorPage struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(anchors, &anchorPage); err != nil || len(anchorPage.Items) != 1 {
		t.Fatal("expected one issuer anchor before restart")
	}
	var saved struct {
		Issuer struct {
			Certificate string `json:"certificate"`
		} `json:"issuer"`
	}
	if err := json.Unmarshal(before, &saved); err != nil || saved.Issuer.Certificate != certPEM {
		t.Fatal("EST certificate did not round-trip")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	start()
	after := call("GET", "/v1/est", nil)
	afterAnchors := call("GET", "/v1/trust-anchors", nil)
	if !bytes.Equal(before, after) || !bytes.Equal(anchors, afterAnchors) || bytes.Contains(after, []byte(keyPEM)) || strings.Contains(string(after), "privateKey") {
		t.Fatal("EST configuration or anchor changed across restart, or key was exposed")
	}
}
