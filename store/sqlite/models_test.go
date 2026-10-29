package sqlite_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"path/filepath"
	"reflect"
	"testing"
)

func TestModels(t *testing.T) {
	storetest.Models(t, func() apiv1.ModelStore {
		d, err := sqlite.Open(filepath.Join(t.TempDir(), "models.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		})
		return d.Models()
	})
}
func TestModelsClosedDatabaseFailsLoudly(t *testing.T) {
	d, err := sqlite.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := d.Models()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func(){
		"list":   func() { s.List("a") },
		"put":    func() { s.Put("a", &model.Object{}) },
		"delete": func() { s.Delete("a", 1, model.V1_0) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("database failure was silently accepted")
				}
			}()
			call()
		})
	}
}

func TestModelsReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := &model.Object{ID: 6000, Name: "persisted", Version: model.Version{Major: 1, Minor: 2}, Resources: map[uint16]*model.Resource{1: {ID: 1, Name: "test", Description: "survives"}}}
	d.Models().Put("a", want)
	d.Models().Put("b", &model.Object{ID: 6000, Version: want.Version, Name: "other"})
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if got := d.Models().List("a"); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("reopen: %#v", got)
	}
	if !d.Models().Delete("a", want.ID, want.Version) || len(d.Models().List("b")) != 1 {
		t.Fatal("reopen tenant deletion")
	}
}
