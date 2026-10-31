package sqlite_test

import (
	"errors"
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
			// A legacy writer can crash after creating an anchor but before saving config.
			if err := anchors.Create("crashed", anchor("orphan")); err != nil {
				t.Fatal(err)
			}
			if err := anchors.Create("crashed", anchor("manual")); err != nil {
				t.Fatal(err)
			}
			if _, err := r.PutConfigRotatingIssuerAnchor("crashed", apiv1.ESTConfig{Certificate: "new"}, anchor("new")); !errors.Is(err, apiv1.ErrESTIssuerOwnershipAmbiguous) {
				t.Fatalf("orphaned issuer must fail closed: %v", err)
			}
			if _, ok := configs.Get("crashed"); ok {
				t.Fatal("config created despite ambiguous anchors")
			}
			if got := anchors.List("crashed"); len(got) != 2 || got[0].ID != "manual" || got[1].ID != "orphan" {
				t.Fatalf("orphaned or manual anchors changed: %+v", got)
			}
			anchors.Delete("crashed", "manual")
			anchors.Delete("crashed", "orphan")
			// Pre-marker stores cannot distinguish an auto-created issuer from a
			// manually created anchor. Do not rotate or delete either silently.
			if err := anchors.Create("legacy", anchor("old")); err != nil {
				t.Fatal(err)
			}
			if err := anchors.Create("legacy", anchor("manual-old")); err != nil {
				t.Fatal(err)
			}
			configs.Put("legacy", apiv1.ESTConfig{Certificate: "old"})
			if _, err := r.PutConfigRotatingIssuerAnchor("legacy", apiv1.ESTConfig{Certificate: "new"}, anchor("new")); !errors.Is(err, apiv1.ErrESTIssuerOwnershipAmbiguous) {
				t.Fatalf("ambiguous legacy issuer must fail closed: %v", err)
			}
			if got, _ := configs.Get("legacy"); got.Certificate != "old" {
				t.Fatalf("legacy config changed: %+v", got)
			}
			if got := anchors.List("legacy"); len(got) != 2 || got[0].ID != "manual-old" || got[1].ID != "old" {
				t.Fatalf("legacy anchors changed: %+v", got)
			}
			anchors.Delete("legacy", "old")
			// The manual anchor remains ambiguous until explicitly reconciled.
			if _, err := r.PutConfigRotatingIssuerAnchor("legacy", apiv1.ESTConfig{Certificate: "new"}, anchor("new")); !errors.Is(err, apiv1.ErrESTIssuerOwnershipAmbiguous) {
				t.Fatalf("manual ownership ambiguity must fail closed: %v", err)
			}
			anchors.Delete("legacy", "manual-old")
			rotate("legacy", "new") // explicit resolution permits upgrade
			anchors.Delete("legacy", "new")
			rotate("mixed", "managed")
			if err := anchors.Create("mixed", anchor("legacy")); err != nil {
				t.Fatal(err)
			}
			configs.Put("mixed", apiv1.ESTConfig{Certificate: "legacy"})
			if _, err := r.PutConfigRotatingIssuerAnchor("mixed", apiv1.ESTConfig{Certificate: "next"}, anchor("next")); !errors.Is(err, apiv1.ErrESTIssuerOwnershipAmbiguous) {
				t.Fatalf("mixed legacy writes must fail closed: %v", err)
			}
			if got := anchors.List("mixed"); len(got) != 2 {
				t.Fatalf("mixed anchors changed: %+v", got)
			}
			anchors.Delete("mixed", "managed")
			anchors.Delete("mixed", "legacy")
			// A legacy writer can crash after adding an unmarked issuer even when
			// the current config still matches a managed issuer.
			rotate("mixed-crash", "a")
			if err := anchors.Create("mixed-crash", anchor("orphan")); err != nil {
				t.Fatal(err)
			}
			if _, err := r.PutConfigRotatingIssuerAnchor("mixed-crash", apiv1.ESTConfig{Certificate: "c"}, anchor("c")); !errors.Is(err, apiv1.ErrESTIssuerOwnershipAmbiguous) {
				t.Fatalf("orphan after managed install must fail closed: %v", err)
			}
			if got, _ := configs.Get("mixed-crash"); got.Certificate != "a" {
				t.Fatalf("config changed after ambiguous rotation: %+v", got)
			}
			if got := anchors.List("mixed-crash"); len(got) != 2 || got[0].ID != "a" || got[1].ID != "orphan" {
				t.Fatalf("anchors changed after ambiguous rotation: %+v", got)
			}
			anchors.Delete("mixed-crash", "a")
			anchors.Delete("mixed-crash", "orphan")
			rotate("x", "a")
			rotate("y", "y")
			manual := anchor("manual")
			if result := anchors.CreateResult("x", "", apiv1.TrustAnchorResult{Anchor: &manual}); result.Problem != nil {
				t.Fatalf("create manual anchor: %+v", result.Problem)
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
