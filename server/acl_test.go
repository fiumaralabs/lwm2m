package server

import (
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/acl"
	"github.com/fiumaralabs/lwm2m/testclient"
)

// Proves: DM-13, DM-14, DM-17
// Against a client enforcing access control (several servers): reads the
// ACL grants succeed, writes it doesn't grant come back 4.01, Create
// needs C on the object's /2 instance and makes this server the owner of
// the new instance's ACL, the owner can set rights, a non-owner cannot,
// and deleting the instance removes its ACL.
func TestAccessControlOwner(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.ShortServerID = 101 })
	c := h.device(testclient.Config{Endpoint: "acl"})
	c.AddObject(16)
	c.EnableAccessControl(101,
		acl.Instance{ID: 0, Object: 3, Instance: 0, Owner: 102, ACL: map[uint16]acl.Rights{0: acl.Read}},
		acl.Instance{ID: 1, Object: 1, Instance: 0, Owner: 101, ACL: map[uint16]acl.Rights{}},
		acl.Instance{ID: 2, Object: 16, Instance: lwm2m.MaxID, Owner: 102, ACL: map[uint16]acl.Rights{101: acl.Create}},
	)
	mustCode(mustRegister(h, c))

	expect(t, "2.05")(h.srv.Read(h.ctx, "acl", p("/3/0/0"), ReadOptions{}))
	expect(t, "4.01")(h.srv.Write(h.ctx, "acl", p("/3/0/13"), []lwm2m.Node{lwm2m.ValueNode(p("/3/0/13"), lwm2m.Time(1))}, WriteOptions{}))
	expect(t, "2.04")(h.srv.Write(h.ctx, "acl", p("/1/0/1"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(500))}, WriteOptions{}))

	expect(t, "2.01")(h.srv.Create(h.ctx, "acl", p("/16"), []lwm2m.Node{lwm2m.ValueNode(p("/16/0/0/0"), lwm2m.String("x"))}, nil))
	insts, err := h.srv.ReadACL(h.ctx, "acl")
	if err != nil {
		t.Fatal(err)
	}
	var created *acl.Instance
	for i := range insts {
		if insts[i].Object == 16 && insts[i].Instance == 0 {
			created = &insts[i]
		}
	}
	if created == nil || created.Owner != 101 || created.ACL[101] != acl.All {
		t.Fatalf("ACL of created instance %+v", insts)
	}
	expect(t, "2.04")(h.srv.SetRights(h.ctx, "acl", created.ID, 102, acl.Read))
	expect(t, "4.01")(h.srv.SetRights(h.ctx, "acl", 0, 101, acl.All)) // /2/0 is owned by 102
	if _, err := h.srv.SetRights(h.ctx, "acl", 0, 101, 32); err == nil {
		t.Fatal("invalid rights sent")
	}

	expect(t, "2.02")(h.srv.Delete(h.ctx, "acl", p("/16/0")))
	insts, _ = h.srv.ReadACL(h.ctx, "acl")
	for _, a := range insts {
		if a.Object == 16 && a.Instance == 0 {
			t.Fatal("ACL of deleted instance kept")
		}
	}
}
