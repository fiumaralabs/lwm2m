package server

import "github.com/fiumaralabs/lwm2m/link"

// Hooks for the tests in package server_test. They run as an external
// package because they drive the core through transport/coap, which
// imports server.

const (
	CodeFETCH    = codeFETCH
	CodeIPATCH   = codeIPATCH
	CodeConflict = codeConflict
)

var (
	NewerNotification = newerNotification
	URIPath           = uriPath
)

func (s *Server) ExpireNow() { s.expireNow() }

func (s *Server) ResolveProfiles(pids []string) ([]link.Object, bool) {
	return s.resolveProfiles(pids)
}

// ObservationsOf counts the observations indexed for a registration ID.
func (s *Server) ObservationsOf(regID string) int { return len(s.obs.forRegistration(regID)) }

func (s *Server) MarkAwake(reg *Registration) bool { return s.queues.markAwake(reg) }

func ObservationToken(o *Observation) []byte { return o.token }

func SetPeer(r *Registration, p Peer) { r.peer = p }
