package testclient

import (
	"github.com/fiumaralabs/lwm2m"
)

// This file holds the hooks a long-running client (cmd/lwm2m-client) needs
// on top of the test API: batch updates, a notification callback and a way
// to stop recording requests.

// SetNodes stores several values at once and then evaluates each affected
// observation once, so a simulated device that changes ten resources of
// /3/0 in one step sends one notification for an Observe of /3/0, not ten.
func (c *Client) SetNodes(nodes []lwm2m.Node) {
	c.mu.Lock()
	var paths []lwm2m.Path
	for _, n := range nodes {
		if n.Kind != lwm2m.KindValue {
			continue
		}
		c.setLocked(n.Path, n.Value)
		paths = append(paths, n.Path)
	}
	seen := map[*observer]bool{}
	var targets []*observer
	for _, o := range c.observers {
		for _, op := range o.paths {
			hit := false
			for _, p := range paths {
				if p.HasPrefix(op) || op.HasPrefix(p) {
					hit = true
					break
				}
			}
			if hit && !seen[o] {
				seen[o] = true
				targets = append(targets, o)
				break
			}
		}
	}
	c.mu.Unlock()
	for _, o := range targets {
		c.evaluate(o)
	}
}

// Notification describes a notification the client sent for an
// observation (pmin/pmax/attribute driven, from fire).
type Notification struct {
	Paths       []lwm2m.Path
	Confirmable bool
	// Reset: the server answered with Reset, ending the observation.
	Reset bool
	Err   error
}

// NotifyHook is called after each attribute-driven notification.
type NotifyHook func(Notification)

// SetNotifyHook installs h (nil removes it).
func (c *Client) SetNotifyHook(h NotifyHook) {
	c.mu.Lock()
	c.notifyHook = h
	c.mu.Unlock()
}

func (c *Client) reportNotify(o *observer, con bool, res NotifyResult, err error) {
	c.mu.Lock()
	h := c.notifyHook
	c.mu.Unlock()
	if h != nil {
		h(Notification{Paths: append([]lwm2m.Path(nil), o.paths...), Confirmable: con, Reset: res == NotifyReset && err == nil && con, Err: err})
	}
}
