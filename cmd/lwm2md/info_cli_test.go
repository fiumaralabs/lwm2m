package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDaemonAdvertisedInfo(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "lwm2md")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	const token = "lwm_test_synthetic"
	keys := filepath.Join(dir, "keys")
	if err := os.WriteFile(keys, []byte(fmt.Sprintf("key_test sha256:%x admin", sha256.Sum256([]byte(token)))), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(1)}, &x509.Certificate{SerialNumber: big.NewInt(1)}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}))
	certPath := filepath.Join(dir, "public.pem")
	if err := os.WriteFile(certPath, []byte(certPEM), 0600); err != nil {
		t.Fatal(err)
	}
	badCert := filepath.Join(dir, "mixed.pem")
	if err := os.WriteFile(badCert, []byte(certPEM+"-----BEGIN PRIVATE KEY-----\nc3ludGhldGlj\n-----END PRIVATE KEY-----\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-server-certificate=" + badCert}, {"-advertise-coap=coaps://example.com"}, {"-advertise-coap=coap://0.0.0.0:5683"}, {"-advertise-coaps=coaps://user:password@example.com"}} {
		if out, err := exec.Command(bin, args...).CombinedOutput(); err == nil {
			t.Fatalf("invalid input accepted: %q", args[0])
		} else if strings.Contains(string(out), "c3ludGhldGlj") || strings.Contains(string(out), "password") {
			t.Fatal("invalid input was exposed in daemon output")
		}
	}
	config := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(config, []byte(fmt.Sprintf(`{"advertise-coap":"coap://file.example:5683","server-certificate":%q}`, certPath)), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name        string
		args        []string
		want        map[string]map[string]string
		certificate string
	}{
		{"configured", []string{"-coap=127.0.0.1:0", "-coaps=127.0.0.1:0", "-advertise-coap=coap://device.example:5683", "-advertise-coaps=coaps://device.example:5684"}, map[string]map[string]string{"server": {"coap": "coap://device.example:5683", "coaps": "coaps://device.example:5684"}}, ""},
		{"absent", []string{"-coap=127.0.0.1:0", "-coaps="}, map[string]map[string]string{}, ""},
		{"disabled", []string{"-coap=", "-coaps=", "-advertise-coap=coap://device.example:5683", "-advertise-coaps=coaps://device.example:5684"}, map[string]map[string]string{}, ""},
		{"file and flag override", []string{"-config=" + config, "-coap=127.0.0.1:0", "-coaps=", "-advertise-coap=coap://override.example:5683"}, map[string]map[string]string{"server": {"coap": "coap://override.example:5683"}}, certPEM},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := ln.Addr().String()
			ln.Close()
			args := []string{"-http=" + addr, "-api-keys=" + keys, "-bs-coap=", "-bs-coaps=", "-bs-http="}
			args = append(args, tc.args...)
			cmd := exec.Command(bin, args...)
			log, err := os.CreateTemp(dir, "daemon-log")
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			cmd.Stdout, cmd.Stderr = log, log
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			client := &http.Client{Timeout: time.Second}
			defer client.CloseIdleConnections()
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				req, _ := http.NewRequest("GET", "http://"+addr+"/v1/info", nil)
				req.Header.Set("Authorization", "Bearer "+token)
				res, err := client.Do(req)
				if err != nil {
					time.Sleep(20 * time.Millisecond)
					continue
				}
				var info struct {
					Endpoints   map[string]map[string]string `json:"endpoints"`
					Certificate *string                      `json:"serverCertificate"`
				}
				err = json.NewDecoder(res.Body).Decode(&info)
				res.Body.Close()
				if err != nil || res.StatusCode != 200 {
					t.Fatalf("status %d decode %v", res.StatusCode, err)
				}
				if !reflect.DeepEqual(info.Endpoints, tc.want) {
					t.Errorf("endpoints = %#v, want %#v", info.Endpoints, tc.want)
				}
				if tc.certificate == "" && info.Certificate != nil {
					t.Error("unexpected server certificate")
				}
				if tc.certificate != "" && (info.Certificate == nil || *info.Certificate != tc.certificate) {
					t.Error("missing or incorrect certificate")
				}
				return
			}
			b, _ := os.ReadFile(log.Name())
			t.Fatalf("daemon unavailable: %s", b)
		})
	}
}
