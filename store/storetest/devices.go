package storetest

import (
	"encoding/base64"
	"github.com/fiumaralabs/lwm2m/store"
	"testing"
	"time"
)

func Devices(t *testing.T, factory func(*testing.T) store.Devices) {
	check := func(ok bool, message string) {
		t.Helper()
		if !ok {
			t.Fatal(message)
		}
	}
	s := factory(t)
	old := time.Unix(1, 0)
	d, err := s.Create("a", store.Device{Endpoint: "b", Name: "sensor", CreatedAt: old, UpdatedAt: old, LastSeenAt: &old})
	check(err == nil && d.CreatedAt.After(old) && d.UpdatedAt.Equal(d.CreatedAt) && d.CreatedAt.Location() == time.UTC && d.LastSeenAt == nil && d.Labels != nil, "create must reset history and normalize labels")
	d.Labels["mutated"] = "yes"
	_, err = s.Create("a", d)
	check(err == store.ErrDeviceExists, "duplicate sentinel")
	_, err = s.Get("other", "b")
	check(err == store.ErrDeviceNotFound, "get isolation")
	check(s.Delete("other", "b") == store.ErrDeviceNotFound, "delete isolation")
	got, err := s.Get("a", "b")
	check(err == nil && len(got.Labels) == 0, "create copy")
	got.Labels["read"] = "mutation"
	later := time.Now().Add(time.Hour)
	s.Seen("other", "b", later.Add(time.Hour))
	s.Seen("a", "missing", later)
	s.Seen("a", "b", later)
	s.Seen("a", "b", old)
	got, _ = s.Get("a", "b")
	check(len(got.Labels) == 0 && got.LastSeenAt != nil && got.LastSeenAt.Equal(later) && got.UpdatedAt.Equal(d.UpdatedAt), "monotonic seen")
	*got.LastSeenAt = old
	got, _ = s.Get("a", "b")
	check(got.LastSeenAt.Equal(later), "lastSeen copy")
	for _, ep := range []string{"c", "a"} {
		_, err = s.Create("a", store.Device{Endpoint: ep, Name: "sensor"})
		check(err == nil, "seed")
	}
	_, err = s.Create("other", store.Device{Endpoint: "b"})
	f := store.DeviceFilter{Q: "sensor", Status: "on", StatusOf: func(store.Device) string { return "on" }}
	page, cursor, err := s.List("a", f, "", 1)
	check(err == nil && len(page) == 1 && page[0].Endpoint == "a" && cursor != "", "filtered sorted first page")
	page[0].Labels["list"] = "mutation"
	got, _ = s.Get("a", "a")
	check(len(got.Labels) == 0, "list copy")
	page, next, err := s.List("a", f, cursor, 500)
	check(err == nil && len(page) == 2 && page[0].Endpoint == "b" && page[1].Endpoint == "c" && next == "", "continuation")
	for _, raw := range []string{"!", "null", `{"After":"a"}`, `{"Tenant":"a","After":"a"}`} {
		_, _, err = s.List("a", store.DeviceFilter{}, base64.RawURLEncoding.EncodeToString([]byte(raw)), 1)
		check(err == store.ErrDeviceCursor, "malformed or incomplete cursor")
	}
	for _, bad := range []struct {
		tenant string
		filter store.DeviceFilter
	}{{"other", f}, {"a", store.DeviceFilter{Q: "changed", Status: "on"}}, {"a", store.DeviceFilter{Q: "sensor", Status: "off"}}} {
		_, _, err = s.List(bad.tenant, bad.filter, cursor, 1)
		check(err == store.ErrDeviceCursor, "cursor binding")
	}
	for _, limit := range []int{0, -1, 501} {
		_, _, err = s.List("a", f, "", limit)
		check(err == store.ErrDeviceCursor, "limit bounds")
	}
	for _, filter := range []store.DeviceFilter{{Q: "b"}, {Status: "on"}, {Q: "absent"}} {
		page, _, err = s.List("a", filter, "", 500)
		want := 0
		if filter.Q == "b" {
			want = 1
		}
		check(err == nil && len(page) == want, "endpoint/status/no-match filtering")
	}
	check(s.Delete("a", "b") == nil, "delete")
	check(s.Delete("a", "b") == store.ErrDeviceNotFound, "delete sentinel")
	_, err = s.Get("other", "b")
	check(err == nil, "delete preserves other tenant")
}
