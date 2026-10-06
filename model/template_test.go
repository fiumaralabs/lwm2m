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
	opq := &Resource{ID: 4, Type: lwm2m.TypeOpaque, Range: "2..3"}
	if opq.CheckValue(lwm2m.Opaque([]byte{1, 2})) != nil || opq.CheckValue(lwm2m.Opaque([]byte{1, 2, 3})) != nil ||
		opq.CheckValue(lwm2m.Opaque([]byte{1})) == nil || opq.CheckValue(lwm2m.Opaque([]byte{1, 2, 3, 4})) == nil {
		t.Fatal("opaque range is an inclusive length in octets")
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
	// An executable carries no value type, even when the XML gives one.
	objs, err := ParseXML([]byte(`<LWM2M><Object><Name>V</Name><ObjectID>30001</ObjectID><ObjectURN>urn:oma:lwm2m:x:30001</ObjectURN>
<MultipleInstances>Single</MultipleInstances><Mandatory>Optional</Mandatory><Resources>
<Item ID="0"><Name>Go</Name><Operations>E</Operations><MultipleInstances>Single</MultipleInstances><Mandatory>Optional</Mandatory><Type>String</Type></Item>
</Resources></Object></LWM2M>`))
	if err != nil || objs[0].Resources[0].Type != lwm2m.TypeNone {
		t.Fatalf("typed executable: %v", err)
	}
	// Empty Operations = Bootstrap-only: /0 resources are neither readable
	// nor writable by a Server, so a Server Write is refused.
	sec, _ := Default().Get(0, Version{1, 2})
	if uri := sec.Resources[0]; uri.Operations != 0 || uri.Readable() || uri.Writable() {
		t.Fatalf("/0/x/0 operations %v", uri.Operations)
	}
	s0 := Default().Schema(map[uint16]Version{0: {1, 2}})
	if err := s0.CheckWrite([]lwm2m.Node{lwm2m.ValueNode(lwm2m.MustParsePath("/0/0/0"), lwm2m.String("coap://x"))}); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("server write of a bootstrap-only resource: %v", err)
	}
	s := Default().Schema(map[uint16]Version{1: {1, 1}})
	err = s.CheckWrite([]lwm2m.Node{lwm2m.ValueNode(lwm2m.MustParsePath("/1/0/1/0"), lwm2m.Integer(60))})
	if !errors.Is(err, ErrNotMultiple) {
		t.Fatalf("resource instance on a single resource: %v", err)
	}
}
