package model

import (
	"errors"
	"testing"

	"github.com/fiumaralabs/lwm2m"
)

// Proves: OBJ-02
// Template rules (C App. D.1): a quoted list is a string enumeration;
// "a..b" is inclusive and a length in octets for String and Opaque; a
// Single object has at most one instance and a Mandatory Single one
// exactly one; executables are Single with no type; a value for a single
// resource cannot carry resource instances.
func TestTemplateRules(t *testing.T) {
	enum := &Resource{ID: 1, Type: lwm2m.TypeString, Range: `"U","UQ","S"`}
	if enum.CheckValue(lwm2m.String("UQ")) != nil || !errors.Is(enum.CheckValue(lwm2m.String("T")), ErrRange) {
		t.Fatal("quoted list is a string enumeration")
	}
	str := &Resource{ID: 2, Type: lwm2m.TypeString, Range: "0..3"}
	if str.CheckValue(lwm2m.String("abc")) != nil || str.CheckValue(lwm2m.String("abcd")) == nil {
		t.Fatal("string range is a length")
	}
	num := &Resource{ID: 3, Type: lwm2m.TypeInteger, Range: "1..5"}
	if num.CheckValue(lwm2m.Integer(1)) != nil || num.CheckValue(lwm2m.Integer(5)) != nil || num.CheckValue(lwm2m.Integer(6)) == nil {
		t.Fatal("inclusive numeric range")
	}
	dev, _ := Default().Get(3, Version{1, 0})
	if dev.CheckInstances(1) != nil || dev.CheckInstances(0) == nil || dev.CheckInstances(2) == nil {
		t.Fatal("/3 is Mandatory Single: exactly one instance")
	}
	srv, _ := Default().Get(1, Version{1, 1})
	if srv.CheckInstances(3) != nil {
		t.Fatal("/1 is Multiple")
	}
	reboot := dev.Resources[4]
	if !reboot.Executable() || reboot.Multiple || reboot.Type != lwm2m.TypeNone {
		t.Fatalf("executable /3/0/4: %+v", reboot)
	}
	s := Default().Schema(map[uint16]Version{1: {1, 1}})
	err := s.CheckWrite([]lwm2m.Node{lwm2m.ValueNode(lwm2m.MustParsePath("/1/0/1/0"), lwm2m.Integer(60))})
	if !errors.Is(err, ErrNotMultiple) {
		t.Fatalf("resource instance on a single resource: %v", err)
	}
}
