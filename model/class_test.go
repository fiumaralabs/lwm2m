package model

import (
	"testing"

	"github.com/fiumaralabs/lwm2m"
)

// Proves: ID-03
// Object ID ranges map to their namespace and URN label (Core App. D.2.1).
func TestObjectClass(t *testing.T) {
	for _, tc := range []struct {
		id    uint16
		class Class
		label string
	}{
		{0, ClassOMA, "oma"}, {1023, ClassOMA, "oma"},
		{1024, ClassReserved, ""}, {2047, ClassReserved, ""},
		{2048, ClassExt, "ext"}, {10240, ClassExt, "ext"},
		{10241, ClassVendor, "x"}, {32768, ClassVendor, "x"},
		{32769, ClassCompanyReserved, "x"}, {42768, ClassCompanyReserved, "x"},
		{42769, ClassTest, "x"}, {42800, ClassTest, "x"},
		{42801, ClassReserved, ""}, {65534, ClassReserved, ""},
		{65535, ClassInvalid, ""},
	} {
		if c := ObjectClass(tc.id); c != tc.class || URNLabel(tc.id) != tc.label {
			t.Errorf("%d: class %v label %q, want %v %q", tc.id, c, URNLabel(tc.id), tc.class, tc.label)
		}
	}
}

// Proves: ID-01
// IDs are 16-bit with 65535 reserved (path parsing, root package tests),
// and the Short Server ID range 1..65534 from the 1.1+ model is enforced
// on values (C §7.4 Tbl 7.4-1).
func TestShortServerIDRange(t *testing.T) {
	o, ok := Default().Get(1, Version{1, 1})
	if !ok {
		t.Fatal("no /1 v1.1")
	}
	ssid := o.Resources[0]
	for _, bad := range []int64{0, 65535} {
		if ssid.CheckValue(lwm2m.Integer(bad)) == nil {
			t.Errorf("SSID %d accepted", bad)
		}
	}
	for _, good := range []int64{1, 65534} {
		if err := ssid.CheckValue(lwm2m.Integer(good)); err != nil {
			t.Errorf("SSID %d: %v", good, err)
		}
	}
}
