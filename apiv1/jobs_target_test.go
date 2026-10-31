package apiv1

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestResolveJobTargetTenantSelectorPagination(t *testing.T) {
	d := &memory.Devices{}
	for i := 0; i < 510; i++ {
		labels := map[string]string{"env": "dev"}
		if i == 3 || i == 509 {
			labels["env"] = "prod"
		}
		if _, err := d.Create("a", store.Device{Endpoint: fmt.Sprintf("d%03d", i), Labels: labels}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Create("b", store.Device{Endpoint: "foreign", Labels: map[string]string{"env": "prod"}}); err != nil {
		t.Fatal(err)
	}
	got, err := resolveJobTarget(context.Background(), d, "a", JobTarget{Selector: "env=prod"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"d003", "d509"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestResolveJobTargetExplicitTenantIsolation(t *testing.T) {
	d := &memory.Devices{}
	if _, err := d.Create("a", store.Device{Endpoint: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Create("b", store.Device{Endpoint: "b"}); err != nil {
		t.Fatal(err)
	}
	got, err := resolveJobTarget(context.Background(), d, "a", JobTarget{Devices: []string{"a", "b", "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
