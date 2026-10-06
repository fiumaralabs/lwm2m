package server

import "github.com/fiumaralabs/lwm2m"

// Event is delivered to Config.OnEvent.
type Event interface{ event() }

// Registered: a client registered. Replaced is the previous registration of
// the same endpoint, removed by this Register (REG-12), or nil.
type Registered struct {
	Registration *Registration
	Replaced     *Registration
}

// Updated: a client sent a successful Update (REG-14).
type Updated struct {
	Registration *Registration
	Previous     Registration // the registration before the update
}

// DeregisterReason says why a registration ended.
type DeregisterReason uint8

const (
	ReasonDeregistered DeregisterReason = iota // client De-register (REG-16)
	ReasonExpired                              // lifetime elapsed (REG-13)
	ReasonReplaced                             // a new Register of the same endpoint (REG-12)
	ReasonRemoved                              // removed by the operator
)

func (r DeregisterReason) String() string {
	return [...]string{"deregistered", "expired", "replaced", "removed"}[r]
}

// Deregistered: a registration ended.
type Deregistered struct {
	Registration *Registration
	Reason       DeregisterReason
}

// Notification: a Notify for an observation (C §6.4.2).
type Notification struct {
	Registration *Registration
	Observation  *Observation
	Response     *Response
}

// SendReceived: a client Send to /dp was accepted (C §6.4.6).
type SendReceived struct {
	Registration  *Registration
	ContentFormat lwm2m.ContentFormat
	Nodes         []lwm2m.Node
}

// Awake: a queue-mode client became reachable (QM-02).
type Awake struct{ Registration *Registration }

func (Registered) event()   {}
func (Updated) event()      {}
func (Deregistered) event() {}
func (Notification) event() {}
func (SendReceived) event() {}
func (Awake) event()        {}
