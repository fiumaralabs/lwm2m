package sqlite_test

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

type issuerRotator interface {
	PutConfigRotatingIssuerAnchor(string, apiv1.ESTConfig, apiv1.TrustAnchor) (bool, error)
}

func TestESTIssuerRotationReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rotation.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	anchor := func(id string) apiv1.TrustAnchor {
		return apiv1.TrustAnchor{ID: id, Certificates: []apiv1.TrustAnchorCertificate{{Fingerprint: id}}}
	}
	if _, err := db.TrustAnchors(10).PutConfigRotatingIssuerAnchor("x", apiv1.ESTConfig{Certificate: "a"}, anchor("a")); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.TrustAnchors(10).PutConfigRotatingIssuerAnchor("x", apiv1.ESTConfig{Certificate: "b"}, anchor("b")); err != nil {
		t.Fatal(err)
	}
	list := db.TrustAnchors(10).List("x")
	if len(list) != 1 || list[0].ID != "b" {
		t.Fatalf("rotation after reopen: %+v", list)
	}
}

func TestESTIssuerRotationContract(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var configs apiv1.ESTConfigStore
			var anchors apiv1.TrustAnchorStore
			if backend == "memory" {
				configs = apiv1.NewMemoryESTConfigStore()
				anchors = apiv1.NewMemoryTrustAnchorStore(4)
			} else {
				db, err := sqlite.Open(filepath.Join(t.TempDir(), "rotation.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				configs = db.ESTConfigs()
				anchors = db.TrustAnchors(4)
			}
			var r issuerRotator
			if backend == "memory" {
				r = apiv1.NewMemoryESTIssuerRotator(configs, anchors)
			} else {
				var ok bool
				r, ok = anchors.(issuerRotator)
				if !ok {
					t.Fatal("anchor store does not support atomic issuer rotation")
				}
			}
			anchor := func(id string) apiv1.TrustAnchor {
				return apiv1.TrustAnchor{ID: id, PEM: id, Certificates: []apiv1.TrustAnchorCertificate{{Fingerprint: id}}}
			}
			rotate := func(tenant, id string) {
				t.Helper()
				if _, err := r.PutConfigRotatingIssuerAnchor(tenant, apiv1.ESTConfig{Certificate: id}, anchor(id)); err != nil {
					t.Fatal(err)
				}
			}
			rotate("x", "a")
			rotate("y", "y")
			if err := anchors.Create("x", anchor("manual")); err != nil {
				t.Fatal(err)
			}
			rotate("x", "b")
			rotate("x", "b")
			if _, ok := anchors.Get("x", "a"); ok {
				t.Fatal("old issuer remains")
			}
			if _, ok := anchors.Get("x", "manual"); !ok {
				t.Fatal("manual anchor removed")
			}
			if _, ok := anchors.Get("y", "y"); !ok {
				t.Fatal("other tenant changed")
			}
			if got := anchors.List("x"); len(got) != 2 {
				t.Fatalf("idempotency: %v", got)
			}
			// Duplicate a manual fingerprint to force insertion failure after the old issuer is removed.
			if _, err := r.PutConfigRotatingIssuerAnchor("x", apiv1.ESTConfig{Certificate: "bad"}, anchor("manual")); err == nil {
				t.Fatal("expected insert failure")
			}
			if got, _ := configs.Get("x"); got.Certificate != "b" {
				t.Fatalf("config changed after failure: %+v", got)
			}
			if _, ok := anchors.Get("x", "b"); !ok {
				t.Fatal("issuer lost on failure")
			}
			var wg sync.WaitGroup
			errs := make(chan error, 16)
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					id := fmt.Sprintf("c%d", i)
					_, err := r.PutConfigRotatingIssuerAnchor("x", apiv1.ESTConfig{Certificate: id}, anchor(id))
					errs <- err
				}(i)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Error(err)
				}
			}
			got, _ := configs.Get("x")
			list := anchors.List("x")
			if len(list) != 2 {
				t.Fatalf("unexpected anchors: %+v", list)
			}
			if _, ok := anchors.Get("x", got.Certificate); !ok {
				t.Fatalf("config does not match anchor: %+v %+v", got, list)
			}
		})
	}
}
