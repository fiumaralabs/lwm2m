package acl

import (
	"testing"

	"github.com/fiumaralabs/lwm2m"
)

// Proves: DM-18
// ACL bits R=1 W=2 E=4 D=8 C=16, other bits invalid; SSID 0 is the default
// entry, 65535 reserved; object IDs 1..65534; /2 instances round-trip.
func TestACLEncoding(t *testing.T) {
	if Read != 1 || Write != 2 || Execute != 4 || Delete != 8 || Create != 16 || All != 31 {
		t.Fatal("bit values")
	}
	if Rights(32).Valid() || !Rights(31).Valid() {
		t.Fatal("range 0..31")
	}
	a := Instance{ID: 4, Object: 3, Instance: 0, Owner: 101, ACL: map[uint16]Rights{0: Read, 102: Read | Write}}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	got, err := FromNodes(a.Nodes())
	if err != nil || len(got) != 1 || got[0].Owner != 101 || got[0].ACL[102] != Read|Write || got[0].ACL[0] != Read {
		t.Fatalf("round trip %+v %v", got, err)
	}
	for _, bad := range []Instance{
		{Object: 0, Owner: 1},
		{Object: lwm2m.MaxID, Owner: 1},
		{Object: 3, Owner: 1, ACL: map[uint16]Rights{1: 32}},
		{Object: 3, Owner: 1, ACL: map[uint16]Rights{lwm2m.MaxID: Read}},
		{Object: 3, Owner: 0},
	} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if _, err := FromNodes([]lwm2m.Node{lwm2m.ValueNode(lwm2m.MustParsePath("/2/0/2"), lwm2m.Integer(1))}); err == nil {
		t.Fatal("single-instance ACL resource accepted")
	}
}

// Proves: DM-15
// Each operation's minimum right (C §8.2 Tbl 8.2-1).
func TestRequiredRights(t *testing.T) {
	for op, want := range map[Operation]Rights{
		OpRead: Read, OpReadComposite: Read, OpObserve: Read, OpObserveComposite: Read, OpWriteAttributes: Read,
		OpWrite: Write, OpWriteComposite: Write, OpDelete: Delete, OpExecute: Execute, OpCreate: Create,
	} {
		if r, ok := Required(op); !ok || r != want {
			t.Errorf("op %d: %v", op, r)
		}
	}
	if _, ok := Required(OpDiscover); ok {
		t.Fatal("Discover needs no right")
	}
}

// Proves: DM-16, DM-13
// Resolution order: own entry, owner without entry = full rights, default
// entry, else nothing; one-server deployments are unrestricted. Object
// level: Write-Attributes and Discover always allowed, Create needs C on
// the object's /2 instance. Read-Composite skips unreadable instances;
// Write-Composite needs W on all of them.
func TestDecide(t *testing.T) {
	insts := []Instance{
		{Object: 3, Instance: 0, Owner: 101, ACL: map[uint16]Rights{102: Read}},
		{Object: 4, Instance: 0, Owner: 101, ACL: map[uint16]Rights{0: Read | Write}},
		{Object: 5, Instance: 0, Owner: 101, ACL: map[uint16]Rights{101: Read}},
		{Object: 6, Instance: lwm2m.MaxID, Owner: 101, ACL: map[uint16]Rights{102: Create}},
	}
	cases := []struct {
		ssid, oid, iid uint16
		want           Rights
	}{
		{101, 3, 0, All},          // owner, no own entry
		{102, 3, 0, Read},         // own entry
		{103, 3, 0, 0},            // no entry, no default
		{103, 4, 0, Read | Write}, // default entry
		{101, 5, 0, Read},         // own entry beats ownership
		{101, 7, 0, 0},            // no /2 instance
	}
	for _, c := range cases {
		if got := Decide(insts, c.ssid, c.oid, c.iid, false); got != c.want {
			t.Errorf("ssid %d on /%d/%d: %v, want %v", c.ssid, c.oid, c.iid, got, c.want)
		}
	}
	if Decide(nil, 1, 3, 0, true) != All {
		t.Fatal("single server must be unrestricted")
	}
	p := lwm2m.MustParsePath
	if !Allowed(insts, 103, OpWriteAttributes, p("/3"), false) || !Allowed(insts, 103, OpDiscover, p("/3/0"), false) {
		t.Fatal("object-level Write-Attributes / Discover refused")
	}
	if !Allowed(insts, 103, OpRead, p("/3"), false) || !Allowed(insts, 103, OpObserve, p("/3"), false) {
		t.Fatal("object-level Read / Observe refused: it aggregates the readable instances (C §8.2.3)")
	}
	if !Allowed(insts, 102, OpCreate, p("/6"), false) || Allowed(insts, 103, OpCreate, p("/6"), false) {
		t.Fatal("Create needs C on /2 of the object")
	}
	if Allowed(insts, 102, OpWrite, p("/3/0/1"), false) || !Allowed(insts, 102, OpRead, p("/3/0/1"), false) {
		t.Fatal("instance-level decision")
	}
	if r := Readable(insts, 102, []lwm2m.Path{p("/3/0"), p("/5/0")}, false); len(r) != 1 || r[0] != p("/3/0") {
		t.Fatalf("readable %v", r)
	}
	if WritableAll(insts, 103, []lwm2m.Path{p("/4/0/1"), p("/3/0/1")}, false) || !WritableAll(insts, 103, []lwm2m.Path{p("/4/0/1")}, false) {
		t.Fatal("write-composite all-or-nothing")
	}
}

// Proves: DM-17, DM-19
// A created instance gets an owner entry with full rights for its creator;
// on unbootstrapping its owner, ownership goes to the server with the
// highest W+D sum, and the instance is deleted when none remains.
func TestOwnership(t *testing.T) {
	a := ForCreated(9, 16, 2, 101)
	if a.Owner != 101 || a.ACL[101] != All || a.Object != 16 || a.Instance != 2 {
		t.Fatalf("%+v", a)
	}
	a.ACL[102] = Read | Write
	a.ACL[103] = Write | Delete
	a.ACL[0] = All
	if o, ok := Unbootstrap(a, 101); !ok || o != 103 {
		t.Fatalf("new owner %d %v, want 103", o, ok)
	}
	solo := ForCreated(1, 16, 3, 101)
	if _, ok := Unbootstrap(solo, 101); ok {
		t.Fatal("orphan instance must be deleted")
	}
}
