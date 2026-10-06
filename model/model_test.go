package model_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"

	lwm2m "github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/model"
)

func v(maj, min uint16) model.Version { return model.Version{Major: maj, Minor: min} }

// Every embedded file parses (Core App. D, VER-04) and its name
// `<id>-<maj>_<min>.xml` agrees with the parsed ObjectID and ObjectVersion
// (LWM2MVersion/ObjectVersion, Core App. J; missing ObjectVersion = 1.0).
// The set is the full version_history/ of objects 0-28 (standards.md §4).
//
// Proves: VER-04
func TestEmbeddedRegistryParses(t *testing.T) {
	nameRE := regexp.MustCompile(`^(\d+)-(\d+)_(\d+)\.xml$`)
	files, err := fs.Glob(model.CoreFS(), "registry/*.xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 54 {
		t.Errorf("embedded files: got %d, want 54", len(files))
	}
	for _, f := range files {
		b, err := fs.ReadFile(model.CoreFS(), f)
		if err != nil {
			t.Fatal(err)
		}
		objs, err := model.ParseXML(b)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		m := nameRE.FindStringSubmatch(path.Base(f))
		if len(objs) != 1 || m == nil {
			t.Errorf("%s: %d objects", f, len(objs))
			continue
		}
		o := objs[0]
		if got := fmt.Sprintf("%d-%d_%d", o.ID, o.Version.Major, o.Version.Minor); got+".xml" != path.Base(f) {
			t.Errorf("%s: parsed as %s", f, got)
		}
		if len(o.Resources) == 0 {
			t.Errorf("%s: no resources", f)
		}
	}
}

// The whole OMNA registry clone (root + version_history/, 500+ objects)
// loads through LoadFS (Core App. D; README §2 "the full OMA registry").
// Set LWM2M_REGISTRY_DIR to the clone; skipped when unset.
func TestFullRegistryDir(t *testing.T) {
	dir := os.Getenv("LWM2M_REGISTRY_DIR")
	if dir == "" {
		t.Skip("LWM2M_REGISTRY_DIR not set")
	}
	r := model.NewRegistry()
	if err := r.LoadFS(os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint16{3303, 3435, 10243, 32666} {
		if len(r.Versions(id)) == 0 {
			t.Errorf("/%d not loaded", id)
		}
	}
}

// Spot-checks against the standards.md §4 object table: resource counts per
// version, instance multiplicity and mandatory flags, and resource types and
// operations from Core App. E.
func TestSection4Table(t *testing.T) {
	r := model.Default()
	counts := []struct {
		id       uint16
		ver      model.Version
		n        int
		multi    bool
		mandator bool
	}{
		{0, v(1, 0), 13, true, true}, {0, v(1, 1), 18, true, true}, {0, v(1, 2), 31, true, true},
		{1, v(1, 0), 9, true, true}, {1, v(1, 1), 24, true, true}, {1, v(1, 2), 28, true, true},
		{2, v(1, 0), 4, true, false}, {2, v(1, 1), 4, true, false},
		{3, v(1, 0), 23, false, true}, {3, v(1, 1), 23, false, true}, {3, v(1, 2), 23, false, true}, {3, v(1, 3), 23, false, true},
		{4, v(1, 0), 11, false, false}, {4, v(1, 1), 12, false, false}, {4, v(1, 2), 13, false, false}, {4, v(1, 3), 14, false, false},
		{5, v(1, 0), 9, false, false}, {5, v(1, 1), 13, false, false}, {5, v(1, 2), 14, false, false},
		{6, v(1, 0), 7, false, false}, {7, v(1, 0), 9, false, false},
		{9, v(1, 0), 19, true, false}, {9, v(1, 1), 23, true, false},
		{20, v(1, 0), 6, false, false}, {20, v(3, 1), 9, true, false},
		{21, v(1, 0), 6, true, false}, {21, v(2, 0), 7, true, false},
		{23, v(1, 0), 3, true, false}, {25, v(2, 0), 3, true, false}, {26, v(1, 0), 2, true, false},
		{27, v(1, 0), 21, true, false}, {28, v(1, 0), 8, false, false},
	}
	for _, c := range counts {
		o, ok := r.Get(c.id, c.ver)
		if !ok || o.Version != c.ver {
			t.Errorf("/%d %s missing", c.id, c.ver)
			continue
		}
		if len(o.Resources) != c.n || o.Multiple != c.multi || o.Mandatory != c.mandator {
			t.Errorf("/%d %s: %d resources multi=%v mandatory=%v, want %d %v %v",
				c.id, c.ver, len(o.Resources), o.Multiple, o.Mandatory, c.n, c.multi, c.mandator)
		}
	}

	if got := fmt.Sprint(r.Versions(5)); got != "[1.0 1.1 1.2]" {
		t.Errorf("/5 versions %s", got)
	}
	if got := fmt.Sprint(r.Versions(20)); got != "[1.0 2.0 2.1 3.0 3.1]" {
		t.Errorf("/20 versions %s", got)
	}
	// /3 1.0 and 1.2 differ only in text: same IDs, types and operations.
	d10, _ := r.Get(3, v(1, 0))
	d12, _ := r.Get(3, v(1, 2))
	for id, a := range d10.Resources {
		b := d12.Resources[id]
		if b == nil || a.Type != b.Type || a.Operations != b.Operations || a.Multiple != b.Multiple {
			t.Errorf("/3/%d differs between 1.0 and 1.2", id)
		}
	}

	type res struct {
		id, ver string
		typ     lwm2m.Type
		ops     model.Operations
		multi   bool
		mand    bool
	}
	for _, c := range []res{
		{"/1/0/0", "1.0", lwm2m.TypeInteger, model.OpRead, false, true},                 // Short Server ID
		{"/1/0/1", "1.0", lwm2m.TypeInteger, model.OpRead | model.OpWrite, false, true}, // Lifetime
		{"/1/0/7", "1.0", lwm2m.TypeString, model.OpRead | model.OpWrite, false, true},  // Binding
		{"/1/0/8", "1.0", lwm2m.TypeNone, model.OpExecute, false, true},                 // Registration Update Trigger
		{"/1/0/11", "1.1", lwm2m.TypeUnsigned, model.OpRead, false, false},              // TLS-DTLS Alert Code
		{"/1/0/13", "1.1", lwm2m.TypeUnsigned, 0, false, false},                         // bootstrap-only
		{"/3/0/4", "1.0", lwm2m.TypeNone, model.OpExecute, false, true},                 // Reboot
		{"/3/0/6", "1.0", lwm2m.TypeInteger, model.OpRead, true, false},                 // Available Power Sources
		{"/3/0/13", "1.0", lwm2m.TypeTime, model.OpRead | model.OpWrite, false, false},  // Current Time
		{"/5/0/0", "1.0", lwm2m.TypeOpaque, model.OpWrite, false, true},                 // Package
		{"/5/0/13", "1.2", lwm2m.TypeUnsigned, model.OpRead | model.OpWrite, false, false},
		{"/0/0/0", "1.0", lwm2m.TypeString, 0, false, true}, // Server URI, bootstrap-only
		{"/21/0/0", "2.0", lwm2m.TypeOpaque, 0, false, true},
	} {
		p := lwm2m.MustParsePath(c.id)
		ver, _ := model.ParseVersion(c.ver)
		o, _ := r.Get(p.Object(), ver)
		got, ok := o.Resource(p.Resource())
		if !ok || got.Type != c.typ || got.Operations != c.ops || got.Multiple != c.multi || got.Mandatory != c.mand {
			t.Errorf("%s v%s: %+v", c.id, c.ver, got)
		}
	}
	if _, ok := r.Get(1, v(1, 0)); !ok {
		t.Fatal()
	}
	if o, _ := r.Get(1, v(1, 0)); o.Resources[11] != nil {
		t.Error("/1 1.0 must not have resource 11")
	}
}

// A missing `ver` means 1.0, or for core objects the version of the client's
// enabler (C §7.2.3). 1.2 table: Core 1.2.2 App. E Tbl E-1 with /5 = 1.1 (A-15).
// An explicit `ver` always wins.
//
// Proves: VER-03
func TestVersionResolution(t *testing.T) {
	for _, c := range []struct {
		lwm2m string
		id    uint16
		ver   string
		want  model.Version
	}{
		// 1.0: everything is 1.0 (C 1.0 has no object versioning).
		{"1.0", 0, "", v(1, 0)}, {"1.0", 3, "", v(1, 0)}, {"1.0", 5, "", v(1, 0)},
		// 1.1.1 (Leshan's table, tie-breaker).
		{"1.1", 0, "", v(1, 1)}, {"1.1", 1, "", v(1, 1)}, {"1.1", 2, "", v(1, 0)}, {"1.1", 3, "", v(1, 1)},
		{"1.1", 4, "", v(1, 2)}, {"1.1", 5, "", v(1, 0)}, {"1.1", 21, "", v(1, 0)},
		// 1.2.x, Tbl E-1.
		{"1.2", 0, "", v(1, 2)}, {"1.2", 1, "", v(1, 2)}, {"1.2", 2, "", v(1, 1)}, {"1.2", 3, "", v(1, 2)},
		{"1.2", 4, "", v(1, 3)}, {"1.2", 5, "", v(1, 1)}, {"1.2", 6, "", v(1, 0)}, {"1.2", 7, "", v(1, 0)},
		{"1.2", 21, "", v(2, 0)}, {"1.2", 23, "", v(1, 0)}, {"1.2", 24, "", v(1, 0)}, {"1.2", 25, "", v(1, 0)},
		{"1.2", 26, "", v(1, 0)}, {"1.2", 27, "", v(1, 0)},
		{"1.2.1", 0, "", v(1, 2)}, {"1.2.2", 21, "", v(2, 0)},
		// Non-core objects default to 1.0 in every enabler.
		{"1.2", 3303, "", v(1, 0)}, {"1.1", 9, "", v(1, 0)}, {"1.2", 20, "", v(1, 0)},
		// Explicit ver wins, including on 1.0 clients (Zephyr sends it).
		{"1.0", 3, "1.1", v(1, 1)}, {"1.2", 5, "1.2", v(1, 2)}, {"1.1", 3303, "1.1", v(1, 1)},
		// Unknown enabler: no core table.
		{"2.0", 0, "", v(1, 0)}, {"bogus", 0, "", v(1, 0)},
	} {
		got, err := model.ResolveVersion(c.lwm2m, c.id, c.ver)
		if err != nil || got != c.want {
			t.Errorf("lwm2m=%s /%d ver=%q: got %s %v, want %s", c.lwm2m, c.id, c.ver, got, err, c.want)
		}
	}
	for _, bad := range []string{"1", "1.", ".1", "1.1.1", "a.b", "1.-1", "99999.0"} {
		if _, err := model.ResolveVersion("1.1", 3, bad); err == nil {
			t.Errorf("ver=%q accepted", bad)
		}
	}
}

// The server ignores minor-version differences within a major (C §7.2.3
// example a): an unknown minor resolves to the closest known one of the same
// major (preferring the highest lower minor); another major never matches.
// URNs are urn:oma:lwm2m:{oma,ext,x}:ID[:major.minor], 1.0 omitted (C §7.2.1).
//
// Proves: VER-02
func TestMinorVersionTolerance(t *testing.T) {
	r := model.Default()
	for _, c := range []struct {
		id        uint16
		ask, want model.Version
		ok        bool
	}{
		{3, v(1, 9), v(1, 3), true},  // newer minor than we know: highest known
		{4, v(1, 7), v(1, 3), true},  //
		{21, v(1, 5), v(1, 1), true}, // 1.x only: never jumps to 2.0
		{21, v(2, 3), v(2, 0), true},
		{20, v(2, 5), v(2, 1), true},
		{3, v(2, 0), model.Version{}, false}, // no 2.x of /3
		{21, v(3, 0), model.Version{}, false},
		{3, v(1, 2), v(1, 2), true}, // exact
	} {
		o, ok := r.Get(c.id, c.ask)
		if ok != c.ok || (ok && o.Version != c.want) {
			t.Errorf("/%d %s: got ok=%v %v, want %v %s", c.id, c.ask, ok, o, c.ok, c.want)
		}
	}
	// Only a higher minor known: use it (type II changes only add items).
	vr := model.NewRegistry()
	if _, err := vr.Register([]byte(vendorXML(30000, "urn:oma:lwm2m:x:30000:1.4", "1.4", rangeItems))); err != nil {
		t.Fatal(err)
	}
	if o, ok := vr.Get(30000, v(1, 1)); !ok || o.Version != v(1, 4) {
		t.Errorf("lower minor did not resolve up: %v", o)
	}

	for _, urn := range []string{"urn:oma:lwm2m:oma:30000", "urn:oma:lwm2m:ext:30000", "urn:oma:lwm2m:x:30000"} {
		if _, err := model.ParseXML([]byte(vendorXML(30000, urn, "", rangeItems))); err != nil {
			t.Errorf("%s rejected: %v", urn, err)
		}
	}
	if o, _ := r.Get(3, v(1, 3)); o.URN != "urn:oma:lwm2m:oma:3:1.3" {
		t.Errorf("URN %q", o.URN)
	}
	if o, _ := r.Get(3, v(1, 0)); o.URN != "urn:oma:lwm2m:oma:3" {
		t.Errorf("URN %q", o.URN)
	}
}

const rangeItems = `
<Item ID="0"><Name>Level</Name><Operations>RW</Operations><MultipleInstances>Single</MultipleInstances><Mandatory>Mandatory</Mandatory><Type>Integer</Type><RangeEnumeration>-10..100</RangeEnumeration><Units>%</Units><Description/></Item>
<Item ID="1"><Name>Label</Name><Operations>RW</Operations><MultipleInstances>Single</MultipleInstances><Mandatory>Optional</Mandatory><Type>String</Type><RangeEnumeration>1..4 bytes</RangeEnumeration><Units/><Description/></Item>
<Item ID="2"><Name>Gain</Name><Operations>W</Operations><MultipleInstances>Single</MultipleInstances><Mandatory>Mandatory</Mandatory><Type>Float</Type><RangeEnumeration>-1.5..1.5</RangeEnumeration><Units/><Description/></Item>
<Item ID="3"><Name>Count</Name><Operations>R</Operations><MultipleInstances>Single</MultipleInstances><Mandatory>Mandatory</Mandatory><Type>Unsigned Integer</Type><RangeEnumeration>0-999</RangeEnumeration><Units/><Description/></Item>
<Item ID="4"><Name>Reset</Name><Operations>E</Operations><MultipleInstances>Single</MultipleInstances><Mandatory>Optional</Mandatory><Type></Type><RangeEnumeration/><Units/><Description/></Item>
<Item ID="5"><Name>Tags</Name><Operations>RW</Operations><MultipleInstances>Multiple</MultipleInstances><Mandatory>Optional</Mandatory><Type>Objlnk</Type><RangeEnumeration/><Units/><Description/></Item>
<Item ID="6"><Name>Blob</Name><Operations>RW</Operations><MultipleInstances>Single</MultipleInstances><Mandatory>Optional</Mandatory><Type>Opaque</Type><RangeEnumeration>0..2</RangeEnumeration><Units/><Description/></Item>
<Item ID="7"><Name>Link</Name><Operations>R</Operations><MultipleInstances>Single</MultipleInstances><Mandatory>Optional</Mandatory><Type>Corelnk</Type><RangeEnumeration/><Units/><Description/></Item>`

func vendorXML(id int, urn, ver, items string) string {
	verEl := ""
	if ver != "" {
		verEl = "<LWM2MVersion>1.1</LWM2MVersion><ObjectVersion>" + ver + "</ObjectVersion>"
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<LWM2M><Object ObjectType="MODefinition"><Name>Vendor</Name><Description1/>
<ObjectID>%d</ObjectID><ObjectURN>%s</ObjectURN>%s
<MultipleInstances>Multiple</MultipleInstances><Mandatory>Optional</Mandatory>
<Resources>%s</Resources><Description2/></Object></LWM2M>`, id, urn, verEl, items)
}

// Runtime registration of a vendor object (C App. D template) and the
// per-client schema codecs use to type values (C App. C): it resolves
// /o/i/r and /o/i/r/ri to the registered version's type and multiplicity.
func TestRegisterAndSchema(t *testing.T) {
	r := model.Default()
	objs, err := r.Register([]byte(vendorXML(30000, "urn:oma:lwm2m:x:30000", "", rangeItems)))
	if err != nil || len(objs) != 1 || objs[0].Version != v(1, 0) || len(objs[0].Resources) != 8 {
		t.Fatalf("%v %v", objs, err)
	}
	o := objs[0]
	if o.Resources[0].Units != "%" || o.Resources[0].Range != "-10..100" || o.Resources[1].Name != "Label" {
		t.Errorf("fields: %+v", o.Resources[0])
	}

	// A 1.0 client: /1 1.0 has no resource 11; a 1.1 client's /1 does.
	var s10, s11 lwm2m.Schema = r.Schema(map[uint16]model.Version{1: v(1, 0), 3: v(1, 0), 30000: v(1, 0)}),
		r.Schema(map[uint16]model.Version{1: v(1, 1), 3: v(1, 1), 5: v(1, 2)})
	for _, c := range []struct {
		s   lwm2m.Schema
		p   string
		def lwm2m.ResourceDef
		ok  bool
	}{
		{s10, "/1/0/1", lwm2m.ResourceDef{Type: lwm2m.TypeInteger}, true},
		{s10, "/1/0/11", lwm2m.ResourceDef{}, false},
		{s11, "/1/0/11", lwm2m.ResourceDef{Type: lwm2m.TypeUnsigned}, true},
		{s10, "/3/0/6", lwm2m.ResourceDef{Type: lwm2m.TypeInteger, Multiple: true}, true},
		{s10, "/3/0/6/1", lwm2m.ResourceDef{Type: lwm2m.TypeInteger, Multiple: true}, true},
		{s10, "/3/0/13", lwm2m.ResourceDef{Type: lwm2m.TypeTime}, true},
		{s10, "/30000/2/3", lwm2m.ResourceDef{Type: lwm2m.TypeUnsigned}, true},
		{s10, "/30000/2/5/9", lwm2m.ResourceDef{Type: lwm2m.TypeObjlnk, Multiple: true}, true},
		{s10, "/30000/2/7", lwm2m.ResourceDef{Type: lwm2m.TypeCorelnk}, true},
		{s11, "/5/0/14", lwm2m.ResourceDef{Type: lwm2m.TypeBoolean}, true},
		{s10, "/5/0/0", lwm2m.ResourceDef{}, false},     // /5 not registered by this client
		{s11, "/30000/0/0", lwm2m.ResourceDef{}, false}, // nor /30000
		{s10, "/3/0", lwm2m.ResourceDef{}, false},       // not a resource path
		{s10, "/3/0/999", lwm2m.ResourceDef{}, false},
	} {
		def, ok := c.s.Resource(lwm2m.MustParsePath(c.p))
		if ok != c.ok || def != c.def {
			t.Errorf("%s: got %+v %v, want %+v %v", c.p, def, ok, c.def, c.ok)
		}
	}
	// A client object with no known definition is simply unknown.
	s := r.Schema(map[uint16]model.Version{3: v(7, 0)})
	if _, ok := s.Object(3); ok {
		t.Error("/3 7.0 resolved")
	}

	// Re-registering the same (ID, version) replaces the definition.
	if _, err := r.Register([]byte(vendorXML(30000, "urn:oma:lwm2m:x:30000", "", rangeItems[:strings.Index(rangeItems, `<Item ID="1">`)]))); err != nil {
		t.Fatal(err)
	}
	if o, _ := r.Get(30000, v(1, 0)); len(o.Resources) != 1 {
		t.Errorf("replace: %d resources", len(o.Resources))
	}
}

// Values are type-checked against the resource (C App. C, DT-02) and the
// "a..b" RangeEnumeration is inclusive, a length in octets for String and
// Opaque (C App. D.1). Free-text ranges are not enforced.
func TestCheckValue(t *testing.T) {
	objs, err := model.ParseXML([]byte(vendorXML(30000, "urn:oma:lwm2m:x:30000", "", rangeItems)))
	if err != nil {
		t.Fatal(err)
	}
	res := objs[0].Resources
	for _, c := range []struct {
		r   uint16
		val lwm2m.Value
		err error
	}{
		{0, lwm2m.Integer(-10), nil}, {0, lwm2m.Integer(100), nil}, {0, lwm2m.Integer(0), nil},
		{0, lwm2m.Integer(101), model.ErrRange}, {0, lwm2m.Integer(-11), model.ErrRange},
		{0, lwm2m.Unsigned(5), model.ErrType}, {0, lwm2m.String("5"), model.ErrType},
		{1, lwm2m.String("a"), nil}, {1, lwm2m.String("abcd"), nil},
		{1, lwm2m.String(""), model.ErrRange}, {1, lwm2m.String("abcde"), model.ErrRange},
		{1, lwm2m.String("é€"), model.ErrRange}, // 5 octets
		{1, lwm2m.String("\xff"), model.ErrType},
		{2, lwm2m.Float(1.5), nil}, {2, lwm2m.Float(-1.6), model.ErrRange}, {2, lwm2m.Integer(1), model.ErrType},
		{3, lwm2m.Unsigned(5000), nil}, // "0-999" is not the a..b form
		{4, lwm2m.Integer(0), model.ErrType},
		{4, lwm2m.Value{}, model.ErrType}, // executables carry no value
		{5, lwm2m.Objlnk(3, 0), nil}, {5, lwm2m.String("3:0"), model.ErrType},
		{6, lwm2m.Opaque([]byte{1, 2}), nil}, {6, lwm2m.Opaque([]byte{1, 2, 3}), model.ErrRange},
		{7, lwm2m.Corelnk("</3>"), nil}, {7, lwm2m.String("</3>"), model.ErrType},
	} {
		err := res[c.r].CheckValue(c.val)
		if !errors.Is(err, c.err) || (c.err == nil) != (err == nil) {
			t.Errorf("res %d %v: got %v, want %v", c.r, c.val, err, c.err)
		}
	}
	// Device /3/0/9 Battery Level "0-100" in the registry: not enforced.
	d, _ := model.Default().Get(3, v(1, 0))
	if err := d.Resources[9].CheckValue(lwm2m.Integer(250)); err != nil {
		t.Error(err)
	}
}

func clientSchema(t *testing.T) *model.Schema {
	t.Helper()
	r := model.Default()
	if _, err := r.Register([]byte(vendorXML(30000, "urn:oma:lwm2m:x:30000", "", rangeItems))); err != nil {
		t.Fatal(err)
	}
	return r.Schema(map[uint16]model.Version{1: v(1, 1), 3: v(1, 0), 5: v(1, 0), 30000: v(1, 0)})
}

func node(p string, val lwm2m.Value) lwm2m.Node { return lwm2m.ValueNode(lwm2m.MustParsePath(p), val) }

// Before a Write the server validates against the object model (DM-06, C
// §6.3.3): known resources, writable, right type and range, resource-instance
// paths only on multi-instance resources.
func TestCheckWrite(t *testing.T) {
	s := clientSchema(t)
	for _, c := range []struct {
		name  string
		nodes []lwm2m.Node
		err   error
	}{
		{"lifetime", []lwm2m.Node{node("/1/0/1", lwm2m.Integer(300))}, nil},
		{"composite", []lwm2m.Node{node("/1/0/1", lwm2m.Integer(300)), node("/3/0/13", lwm2m.Time(1e9)), node("/5/0/0", lwm2m.Opaque([]byte{1}))}, nil},
		{"write-only", []lwm2m.Node{node("/30000/0/2", lwm2m.Float(0))}, nil},
		{"multi instance", []lwm2m.Node{node("/30000/0/5/3", lwm2m.Objlnk(3, 0))}, nil},
		{"empty multiple", []lwm2m.Node{{Path: lwm2m.MustParsePath("/30000/0/5"), Kind: lwm2m.KindEmptyMultiple}}, nil},
		{"empty instance", []lwm2m.Node{{Path: lwm2m.MustParsePath("/30000/0"), Kind: lwm2m.KindEmptyInstance}}, nil},
		{"read-only", []lwm2m.Node{node("/1/0/0", lwm2m.Integer(1))}, model.ErrNotWritable},
		{"read-only in composite", []lwm2m.Node{node("/1/0/1", lwm2m.Integer(300)), node("/3/0/0", lwm2m.String("x"))}, model.ErrNotWritable},
		{"bootstrap-only", []lwm2m.Node{node("/1/0/13", lwm2m.Unsigned(1))}, model.ErrNotWritable},
		{"executable", []lwm2m.Node{node("/1/0/8", lwm2m.Value{})}, model.ErrType},
		{"type", []lwm2m.Node{node("/1/0/1", lwm2m.String("300"))}, model.ErrType},
		{"range", []lwm2m.Node{node("/30000/0/0", lwm2m.Integer(1000))}, model.ErrRange},
		{"unknown resource", []lwm2m.Node{node("/1/0/99", lwm2m.Integer(1))}, model.ErrUnknownResource},
		{"unknown object", []lwm2m.Node{node("/4/0/1", lwm2m.Integer(1))}, model.ErrUnknownObject},
		{"ri on single", []lwm2m.Node{node("/1/0/1/0", lwm2m.Integer(1))}, model.ErrNotMultiple},
		{"empty multiple on single", []lwm2m.Node{{Path: lwm2m.MustParsePath("/1/0/1"), Kind: lwm2m.KindEmptyMultiple}}, model.ErrNotMultiple},
	} {
		err := s.CheckWrite(c.nodes)
		if !errors.Is(err, c.err) || (c.err == nil) != (err == nil) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.err)
		}
	}
	if err := s.CheckWrite([]lwm2m.Node{node("/30000/0/5", lwm2m.Objlnk(3, 0))}); err == nil {
		t.Error("value on a multi-instance resource path accepted")
	}
}

// Create carries all mandatory resources (DM-09, C §6.3.6). The server must
// supply the mandatory writable ones; read-only values may be present (the
// client ignores them) but still type-check.
func TestCheckCreate(t *testing.T) {
	s := clientSchema(t)
	full := []lwm2m.Node{node("/1/0/1", lwm2m.Integer(300)), node("/1/0/6", lwm2m.Boolean(true)), node("/1/0/7", lwm2m.String("U"))}
	if err := s.CheckCreate(1, full); err != nil {
		t.Errorf("full /1: %v", err)
	}
	if err := s.CheckCreate(1, append(full, node("/1/0/0", lwm2m.Integer(5)))); err != nil {
		t.Errorf("read-only SSID in create: %v", err)
	}
	if err := s.CheckCreate(1, full[1:]); !errors.Is(err, model.ErrMissing) {
		t.Errorf("missing lifetime: %v", err)
	}
	if err := s.CheckCreate(1, append(full, node("/1/0/0", lwm2m.String("5")))); !errors.Is(err, model.ErrType) {
		t.Errorf("bad read-only type: %v", err)
	}
	if err := s.CheckCreate(1, append(full, node("/3/0/13", lwm2m.Time(0)))); err == nil {
		t.Error("foreign object node accepted")
	}
	if err := s.CheckCreate(4, nil); !errors.Is(err, model.ErrUnknownObject) {
		t.Errorf("unregistered object: %v", err)
	}
	// /30000: mandatory writable 0 (RW) and 2 (W); mandatory R 3 is the client's.
	if err := s.CheckCreate(30000, []lwm2m.Node{node("/30000/7/0", lwm2m.Integer(1)), node("/30000/7/2", lwm2m.Float(0))}); err != nil {
		t.Errorf("vendor: %v", err)
	}
	if err := s.CheckCreate(30000, []lwm2m.Node{node("/30000/7/0", lwm2m.Integer(1))}); !errors.Is(err, model.ErrMissing) {
		t.Errorf("vendor missing /2: %v", err)
	}
}

// Execute targets a Single executable resource, /o/i/r (C §6.3.5, App. D.1).
func TestCheckExecute(t *testing.T) {
	s := clientSchema(t)
	for p, want := range map[string]error{
		"/1/0/8":     nil,
		"/3/0/4":     nil,
		"/30000/1/4": nil,
		"/1/0/1":     model.ErrNotExecutable,
		"/5/0/0":     model.ErrNotExecutable,
		"/1/0/99":    model.ErrUnknownResource,
		"/4/0/0":     model.ErrUnknownResource,
	} {
		err := s.CheckExecute(lwm2m.MustParsePath(p))
		if !errors.Is(err, want) || (want == nil) != (err == nil) {
			t.Errorf("%s: got %v, want %v", p, err, want)
		}
	}
	if err := s.CheckExecute(lwm2m.MustParsePath("/3/0")); err == nil {
		t.Error("instance path accepted")
	}
}

// Documents that break LWM2M.xsd / LWM2M-v1_1.xsd (C App. D) or the ID and
// URN rules (C §7.3, §7.2.1) are rejected with ErrInvalidXML.
//
// Proves: VER-02, VER-04
func TestMalformedXMLRejected(t *testing.T) {
	item := func(id, ops, multi, mand, typ string) string {
		return fmt.Sprintf(`<Item ID="%s"><Name>x</Name><Operations>%s</Operations><MultipleInstances>%s</MultipleInstances><Mandatory>%s</Mandatory><Type>%s</Type><RangeEnumeration/><Units/><Description/></Item>`, id, ops, multi, mand, typ)
	}
	good := item("0", "R", "Single", "Optional", "String")
	ok := vendorXML(30000, "urn:oma:lwm2m:x:30000", "", good)
	if _, err := model.ParseXML([]byte(ok)); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	for name, doc := range map[string]string{
		"not xml":             "hello",
		"truncated":           ok[:len(ok)-20],
		"wrong root":          `<Foo><Object/></Foo>`,
		"no object":           `<LWM2M></LWM2M>`,
		"missing ObjectID":    strings.Replace(ok, "<ObjectID>30000</ObjectID>", "", 1),
		"ObjectID MAX_ID":     vendorXML(65535, "urn:oma:lwm2m:x:65535", "", good),
		"ObjectID negative":   strings.Replace(ok, "<ObjectID>30000", "<ObjectID>-1", 1),
		"object multiplicity": strings.Replace(ok, "<MultipleInstances>Multiple</MultipleInstances><Mandatory>Optional", "<MultipleInstances>Many</MultipleInstances><Mandatory>Optional", 1),
		"object mandatory":    strings.Replace(ok, "</MultipleInstances><Mandatory>Optional</Mandatory>\n", "</MultipleInstances><Mandatory>Yes</Mandatory>\n", 1),
		"no Resources":        strings.Replace(ok, "<Resources>"+good+"</Resources>", "", 1),
		"bad ObjectVersion":   vendorXML(30000, "urn:oma:lwm2m:x:30000:1", "1", good),
		"URN id mismatch":     vendorXML(30000, "urn:oma:lwm2m:x:30001", "", good),
		"URN namespace":       vendorXML(30000, "urn:oma:lwm2m:foo:30000", "", good),
		"URN ver mismatch":    vendorXML(30000, "urn:oma:lwm2m:x:30000:1.1", "1.2", good),
		"URN ver omitted":     vendorXML(30000, "urn:oma:lwm2m:x:30000", "2.0", good),
		"item ID missing":     vendorXML(30000, "urn:oma:lwm2m:x:30000", "", strings.Replace(good, ` ID="0"`, "", 1)),
		"item ID MAX_ID":      vendorXML(30000, "urn:oma:lwm2m:x:30000", "", item("65535", "R", "Single", "Optional", "String")),
		"item ID text":        vendorXML(30000, "urn:oma:lwm2m:x:30000", "", item("a", "R", "Single", "Optional", "String")),
		"duplicate item":      vendorXML(30000, "urn:oma:lwm2m:x:30000", "", good+good),
		"operations":          vendorXML(30000, "urn:oma:lwm2m:x:30000", "", item("0", "RX", "Single", "Optional", "String")),
		"operations missing":  vendorXML(30000, "urn:oma:lwm2m:x:30000", "", strings.Replace(good, "<Operations>R</Operations>", "", 1)),
		"item multiplicity":   vendorXML(30000, "urn:oma:lwm2m:x:30000", "", item("0", "R", "", "Optional", "String")),
		"item mandatory":      vendorXML(30000, "urn:oma:lwm2m:x:30000", "", item("0", "R", "Single", "Must", "String")),
		"type":                vendorXML(30000, "urn:oma:lwm2m:x:30000", "", item("0", "R", "Single", "Optional", "Double")),
		"type none literal":   vendorXML(30000, "urn:oma:lwm2m:x:30000", "", item("0", "E", "Single", "Optional", "none")),
		"type missing":        vendorXML(30000, "urn:oma:lwm2m:x:30000", "", strings.Replace(good, "<Type>String</Type>", "", 1)),
	} {
		_, err := model.ParseXML([]byte(doc))
		if !errors.Is(err, model.ErrInvalidXML) {
			t.Errorf("%s: got %v", name, err)
		}
		if _, err := model.NewRegistry().Register([]byte(doc)); err == nil {
			t.Errorf("%s: Register accepted", name)
		}
	}
	// Tolerated registry deviations (README §1.3): an E item with a type or
	// Multiple loads with its type dropped (registry 15-1_0 /6, 10260-1_0 /3).
	objs, err := model.ParseXML([]byte(vendorXML(30000, "urn:oma:lwm2m:x:30000", "", item("0", "E", "Multiple", "Optional", "String"))))
	if err != nil || objs[0].Resources[0].Type != lwm2m.TypeNone {
		t.Errorf("tolerated E item: %v", err)
	}
}
